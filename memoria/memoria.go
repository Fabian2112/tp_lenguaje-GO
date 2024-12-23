package main

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log"
	"log/slog"
	"memoria/utils"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

type BodyRequest struct {
	PID  int `json:"pid"`
	Size int `json:"size"`
}

type MensajeCompactacion struct {
	PID         int    `json:"pid"`
	Accion      string `json:"accion"` // "compactar" o "fallo"
	Descripcion string `json:"descripcion"`
}

// Definir un mapa para almacenar múltiples espacioMemoria
// memoria del sistema
var mapaContextosProcesos = make(map[int]utils.ProcessContext)
var mapaRegistrosHilos = make(map[int][]utils.ThreadContext)
var mapaInstrucciones = make(map[int][]string)

// memoria usuario
var Logger *slog.Logger
var config utils.MemoriaConfig
var particiones []utils.EspacioMemoria
var memoriaUsuario []byte
var muInstrucciones sync.Mutex
var muContextoProcesos sync.Mutex
var muRegistroHilos sync.Mutex
var muMemoria sync.Mutex
var muParciones sync.Mutex

func main() {
	// Inicialización de la configuración de memoria
	utils.IniciarConfiguracionMemoria(&config, "utils/config.json")
	InicializarMemoria()
	// Configuración de los endpoints y rutas HTTP directamente en el main
	mux := http.NewServeMux()

	mux.HandleFunc("/createProceso", createProceso)
	mux.HandleFunc("/CompactarMemoria", compactarMemoria) //falta lo de compatacion
	mux.HandleFunc("/finalizarProceso", finalizarProceso)
	mux.HandleFunc("/crearHilo", crearHilo)
	mux.HandleFunc("/endHilo", endHilo)
	mux.HandleFunc("/memoryDump", memoryDump)
	mux.HandleFunc("/obtenerContextoEjecucion", obtenerContextoEjecucion)
	mux.HandleFunc("/actualizarContextoEjecucion", actualizarContextoEjecucion)
	mux.HandleFunc("/obtenerInstruccion", obtenerInstruccion)
	mux.HandleFunc("/readMem", readMem)
	mux.HandleFunc("/writeMem", writeMem)

	log.Println("El Memoria server está a la escucha en el puerto", fmt.Sprint(config.Port))
	err := http.ListenAndServe(fmt.Sprint(":", config.Port), mux)
	if err != nil {
		panic(err)
	}
}

func InicializarMemoria() {
	memoriaUsuario = make([]byte, config.Memory_Size)

	if config.Scheme == "FIJAS" {

		// Inicializar particiones fijas desde la configuración
		inicio := 0
		for _, size := range config.Partitions {
			particion := utils.EspacioMemoria{
				Base:   inicio,
				Size:   size,
				IsFree: true,
				Limit:  inicio + size - 1,
				PID:    -1,
			}
			particiones = append(particiones, particion)
			inicio += size
		}

	} else {
		// Inicializar con una única partición dinámica
		particion := utils.EspacioMemoria{
			Base:   0,
			Size:   config.Memory_Size,
			IsFree: true,
			Limit:  config.Memory_Size - 1,
			PID:    -1,
		}
		particiones = append(particiones, particion)
	}

	utils.MostrarMemoriaConfig(config)

}

func createProceso(w http.ResponseWriter, r *http.Request) {

	defer r.Body.Close()

	var request BodyRequest
	err := json.NewDecoder(r.Body).Decode(&request)
	if err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	pid := request.PID
	size := request.Size

	muContextoProcesos.Lock()
	defer muContextoProcesos.Unlock()

	// Se libera el lock sin importar si hay éxito o error

	if base, limit, ok := AsignarHueco(w, pid, size, config); ok {

		mapaContextosProcesos[pid] = utils.ProcessContext{
			Base:  base,
			Limit: limit,
		}

		w.WriteHeader(http.StatusOK)
		w.Write([]byte("Proceso creado OK"))
		Logger.Info("## Proceso creado - PID: %d - Tamaño: %d\n", pid, size)

		for _, p := range particiones {
			log.Printf("## Proceso SI - PID: %d - Tamaño: %d - Está libre: %t\n", p.PID, p.Size, p.IsFree)
		}

	} else {
		for _, p := range particiones {
			log.Printf("## Proceso NO - PID: %d - Tamaño: %d - Está libre: %t\n", p.PID, p.Size, p.IsFree)
		}
		http.Error(w, "No se pudo inicializar el proceso", http.StatusInternalServerError)

		return // No se puede continuar si no se pudo asignar el hueco
	}
}

func AsignarHueco(w http.ResponseWriter, pid int, size int, config utils.MemoriaConfig) (int, int, bool) {
	muParciones.Lock()
	defer muParciones.Unlock()

	var index = -1
	var bestSize = config.Memory_Size + 1 // Para el algoritmo Best Fit
	var worstSize = -1                    // Para el algoritmo Worst Fit
	var base int
	var totalHuecosLibres int

	if config.Scheme == "FIJAS" {
		// Algoritmo para particiones fijas
		for i, p := range particiones {
			if p.IsFree && p.Size >= size {
				switch config.Search_algorithm {
				case "FIRST":
					index = i
					break
				case "BEST":
					if p.Size < bestSize {
						index = i
						bestSize = p.Size
					}
				case "WORST":
					if p.Size > worstSize {
						index = i
						worstSize = p.Size
					}
				}
			}
			if index != -1 && config.Search_algorithm == "FIRST" {
				break // Salir del bucle si se encuentra un espacio con First Fit
			}
		}

		if index == -1 {
			// No se encontró un hueco en particiones fijas
			log.Println("No se pudo inicializar el proceso: No hay espacio en particiones fijas.")
			return -1, -1, false
		}
		log.Println("index: ", index)
	} else {
		// Algoritmo para partición dinámica
		for i, p := range particiones {
			if p.IsFree {
				totalHuecosLibres += p.Size
				if p.Size >= size {
					switch config.Search_algorithm {
					case "FIRST":
						index = i
						break
					case "BEST":
						if p.Size < bestSize {
							index = i
							bestSize = p.Size
						}
					case "WORST":
						if p.Size > worstSize {
							index = i
							worstSize = p.Size
						}
					}
				}
			}
			if index != -1 && config.Search_algorithm == "FIRST" {
				break // Salir del bucle si se encuentra un espacio con First Fit
			}
		}
		if index == -1 {
			// No se encontró un hueco adecuado, verificar si hay espacio total suficiente
			if totalHuecosLibres >= size {
				log.Println("Se necesita compactar la memoria para asignar el proceso.")
				w.WriteHeader(http.StatusBadRequest)
				w.Write([]byte("compactar"))
				return -1, -1, false // No agregues más llamadas a w después
			} else {
				log.Println("No se pudo inicializar el proceso: No hay suficiente espacio en los huecos libres.")
				return -1, -1, false
			}
		}

	}

	// Asignar la partición seleccionada
	particiones[index].IsFree = false
	base = particiones[index].Base
	limit := base + size - 1
	particiones[index].PID = pid

	// Ajustar la partición si es dinámica (y si la partición es más grande que el proceso)
	if config.Scheme == "DINAMICAS" && particiones[index].Size > size {
		nuevaParticion := utils.EspacioMemoria{
			Base:   base + size,                               // La base de la nueva partición libre será después del proceso asignado
			Size:   particiones[index].Size - size,            // El tamaño de la nueva partición libre será el sobrante
			IsFree: true,                                      // Es una partición libre
			Limit:  base + particiones[index].Size - size - 1, // El límite de la nueva partición libre
		}

		// Redefinir el tamaño de la partición original para ajustarse al tamaño del proceso
		particiones[index].Size = size

		// Agregar la nueva partición libre después de la partición actual
		particiones = append(particiones[:index+1], append([]utils.EspacioMemoria{nuevaParticion}, particiones[index+1:]...)...)
	}

	// Imprimir los resultados
	log.Printf("Proceso asignado a base: %d, limite: %d, PID: %d", base, limit, pid)

	return base, limit, true
}

func compactarMemoria(w http.ResponseWriter, r *http.Request) {
	// Ordenar las particiones para mover las libres hacia un lado (esto es una forma de compactar)
	// Suponiendo que las particiones libres se mueven al final de la memoria
	var particionesOcupadas []utils.EspacioMemoria
	inicio := 0
	pedazoLibre := 0

	muParciones.Lock()
	defer muParciones.Unlock()

	// Dividir las particiones entre libres y ocupadas
	for _, particion := range particiones {
		if particion.IsFree {
			pedazoLibre += particion.Size

		} else {
			particion.Base = inicio
			particion.Limit = inicio + particion.Size - 1

			inicio += particion.Size
			cambiarContextoProceso(particion.Base, particion.Limit, particion.PID)

			particionesOcupadas = append(particionesOcupadas, particion)
		}
	}

	if pedazoLibre == 0 {
		fmt.Println("La memoria ya está compactada, no hay particiones libres")
		return
	}

	nuevaParticionLibre := utils.EspacioMemoria{
		Base:   inicio,
		Size:   pedazoLibre,
		IsFree: true,
		Limit:  inicio + pedazoLibre - 1,
		PID:    -1,
	}

	fmt.Println("Memoria compactada exitosamente")

	// Reorganizar las particiones, primero las ocupadas y luego las libres
	particiones = append(particionesOcupadas, nuevaParticionLibre)

}

func cambiarContextoProceso(base int, limit int, pid int) {
	muContextoProcesos.Lock()
	defer muContextoProcesos.Unlock()

	// Buscar el contexto del proceso usando el PID
	contexto, exists := mapaContextosProcesos[pid]
	if !exists {
		log.Printf("Contexto del proceso PID %d no encontrado.\n", pid)
		return
	}

	// Actualizar el contexto del proceso con los nuevos valores
	contexto.Base = base
	contexto.Limit = limit

	// Actualizar el mapa con el nuevo contexto
	mapaContextosProcesos[pid] = contexto

	// Informar sobre el cambio de contexto
	fmt.Printf("Contexto del proceso PID %d actualizado: Base = %d, Limit = %d\n", pid, base, limit)
}

func finalizarProceso(w http.ResponseWriter, r *http.Request) {
	// Responder con un mensaje en el cuerpo de la respuesta

	w.Header().Set("Content-Type", "application/json")

	// Crear una estructura para recibir los datos del cuerpo de la solicitud
	type RequestBody struct {
		PID int `json:"pid"` // El PID que se enviará en el cuerpo
	}

	// Leer el cuerpo de la solicitud
	var request RequestBody
	err := json.NewDecoder(r.Body).Decode(&request)
	if err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	muContextoProcesos.Lock()
	defer muContextoProcesos.Unlock()
	defer r.Body.Close() // Se libera el lock sin importar si hay éxito o error

	pid := request.PID // Obtener el PID desde el body

	// Variable para saber si se encontró y liberó la partición
	particionLiberada, err := liberarParticionPorPID(pid)

	if err != nil {
		log.Printf("No se encontró el proceso con PID %d en la memoria.\n", pid)
		// Responder con un mensaje en el cuerpo de la respuesta
		w.WriteHeader(http.StatusNotFound)
		response := map[string]string{
			"error": "Proceso no encontrado",
		}
		json.NewEncoder(w).Encode(response)
		return
	}

	if particionLiberada == nil {
		log.Printf("No se encontró el proceso con PID %d en la memoria.\n", pid)
		// Si no se encuentra la partición, enviar un error con el mensaje apropiado
		for _, p := range particiones {
			log.Printf("## Proceso SI - PID: %d - Tamaño: %d - Está libre: %t\n", p.PID, p.Size, p.IsFree)
		}
		http.Error(w, "Proceso no encontrado", http.StatusNotFound)
		return
	}

	// Si el proceso fue encontrado y liberado, enviar un mensaje de éxito
	w.WriteHeader(http.StatusOK)
	response := map[string]string{
		"message": fmt.Sprintf("Proceso con PID %d finalizado correctamente.", pid),
	}
	json.NewEncoder(w).Encode(response)
	Logger.Info("## Proceso destruido - PID: %d - Tamaño: %d\n", pid, particionLiberada.Size)

	if config.Scheme == "DINAMICAS" {
		ConsolidarMemoria()
	}
}

func ConsolidarMemoria() {
	muParciones.Lock()
	defer muParciones.Unlock()

	// Iterar sobre las particiones y consolidar las adyacentes que estén libres
	for i := 0; i < len(particiones)-1; {
		// Si la partición actual y la siguiente están libres, se pueden fusionar
		if particiones[i].IsFree && particiones[i+1].IsFree {
			// Combinar las particiones
			particiones[i].Size += particiones[i+1].Size
			particiones[i].Limit = particiones[i+1].Limit

			// Eliminar la partición adyacente después de la fusión
			particiones = append(particiones[:i+1], particiones[i+2:]...)
		} else {
			// Solo avanzar al siguiente índice si no se consolidó
			i++
		}
	}
	log.Println("Consolidación de memoria completada.")
}

func liberarParticionPorPID(pid int) (*utils.EspacioMemoria, error) {
	muParciones.Lock()
	defer muParciones.Unlock()
	for i := 0; i < len(particiones); i++ {
		if particiones[i].PID == pid {
			particiones[i].IsFree = true // Marcar la partición como libre
			particiones[i].PID = -1
			delete(mapaContextosProcesos, pid) // Eliminar el proceso del mapa
			log.Printf("## Proceso %s - PID: %d - Tamaño: %d\n", "Destruido", pid, particiones[i].Size)
			return &particiones[i], nil
		}
	}
	return nil, fmt.Errorf("Proceso con PID %d no encontrado", pid)
}

func crearHilo(w http.ResponseWriter, r *http.Request) {
	// Leer el PID y TID desde los parámetros de la URL

	defer r.Body.Close()

	type RequestBody struct {
		PID  int    `json:"pid"`
		TID  int    `json:"tid"` // El PID y TID que se enviarán en el cuerpo
		FILE string `json:"file"`
	}
	// Leer el cuerpo de la solicitud
	var request RequestBody
	err := json.NewDecoder(r.Body).Decode(&request)
	if err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	fmt.Println("Request file:", request.FILE)

	pid := request.PID
	tid := request.TID
	relativePath := filepath.Join(config.Instructions_Path, request.FILE)

	fileContent, err := os.ReadFile(relativePath)
	if err != nil {
		http.Error(w, fmt.Sprintf("Error reading file: %v", err), http.StatusInternalServerError)
		return
	}

	// Extraer las instrucciones del contenido del archivo
	instrucciones := strings.Split(string(fileContent), "\n")
	// Eliminar líneas vacías de forma eficiente
	var instruccionesFiltradas []string
	for _, linea := range instrucciones {
		if linea != "" {
			instruccionesFiltradas = append(instruccionesFiltradas, linea)
		}
	}
	instrucciones = instruccionesFiltradas

	// Actualizar el mapa de instrucciones
	muInstrucciones.Lock()
	mapaInstrucciones[tid] = instrucciones
	muInstrucciones.Unlock()

	nuevoThreadContext := utils.ThreadContext{
		TID: tid,
		RegistroHilos: utils.RegistroHilos{
			AX: 0,
			BX: 0,
			CX: 0,
			DX: 0,
			EX: 0,
			FX: 0,
			GX: 0,
			HX: 0,
			PC: 0,
		}, // Program Counter inicializado en 0
	}

	// Asegurarse de que el mapa esté inicializado
	if _, exists := mapaRegistrosHilos[pid]; !exists {
		mapaRegistrosHilos[pid] = []utils.ThreadContext{}
	}

	// Añadir el nuevo contexto de hilo al mapa
	muRegistroHilos.Lock()
	mapaRegistrosHilos[pid] = append(mapaRegistrosHilos[pid], nuevoThreadContext)
	muRegistroHilos.Unlock()

	// Responder como OK
	Logger.Info("## Hilo %s - (PID:TID) - (%d:%d)\n", "creado", pid, tid)
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "OK"})
}

func endHilo(w http.ResponseWriter, r *http.Request) {

	type RequestBody struct {
		PID int `json:"pid"`
		TID int `json:"tid"`
	}

	// Leer el cuerpo de la solicitud
	var request RequestBody
	err := json.NewDecoder(r.Body).Decode(&request)
	if err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	pid := request.PID
	tid := request.TID

	// Eliminar el hilo del mapa de registros
	if threadContexts, exists := mapaRegistrosHilos[pid]; exists {
		for i, threadContext := range threadContexts {
			if threadContext.TID == tid {
				// Desplaza los elementos hacia la izquierda en el slice
				threadContexts = append(threadContexts[:i], threadContexts[i+1:]...)
				mapaRegistrosHilos[pid] = threadContexts
				//log.Printf("ThreadContext con tid %d eliminado\n", tid)
				Logger.Info("## Hilo destruido - (PID:TID) - (%d:%d)\n", pid, tid)
				// Responder con OK
				w.WriteHeader(http.StatusOK)
				w.Write([]byte("Hilo finalizado OK"))
				return
			}
		}
		// Si el hilo no se encuentra
		http.Error(w, "Hilo no encontrado", http.StatusNotFound)
	} else {
		// Si el PID no existe
		http.Error(w, "PID no encontrado", http.StatusNotFound)
	}
}

func memoryDump(w http.ResponseWriter, r *http.Request) {

	var requestData struct {
		PID int `json:"pid"`
		TID int `json:"tid"`
	}

	err := json.NewDecoder(r.Body).Decode(&requestData)
	if err != nil {
		http.Error(w, "Error al decodificar el cuerpo de la solicitud", http.StatusBadRequest)
		return
	}

	pid := requestData.PID
	tid := requestData.TID

	tamaño, contenido := obtenerDump(pid, tid)
	if contenido == nil {
		http.Error(w, "No se encontró el proceso", http.StatusNotFound)
		return
	}

	// Generar el timestamp actual
	timestamp := getCurrentTimestamp()

	// Crear el nombre del archivo
	nombre := fmt.Sprintf("%d-%d-%d.dmp", pid, tid, timestamp)

	log.Println("Se crea el nombre", nombre)

	ok, err := mandarAlFS(nombre, tamaño, contenido)

	if !ok {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	} else {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("Dump creado correctamente"))
		Logger.Info("## Memory Dump - (PID:TID) - (%d:%d) - Archivo: %s\n", pid, tid, nombre)
	}

}

func obtenerDump(pid int, tid int) (int, []byte) {
	muMemoria.Lock()
	defer muMemoria.Unlock()

	// Buscar el contexto del proceso usando el PID
	contexto, existe := mapaContextosProcesos[pid]
	if !existe {
		log.Printf("Proceso con PID %d no encontrado.\n", pid)
		return 0, nil
	}

	// Calcular el tamaño del dump
	tamaño := contexto.Limit - contexto.Base + 1

	// Crear un slice para almacenar el contenido del dump
	contenido := make([]byte, tamaño)
	log.Println("es el cotenido", contenido)

	// Copiar el contenido de la memoria del proceso al slice
	copy(contenido, memoriaUsuario[contexto.Base:contexto.Limit+1])

	log.Printf("Dump obtenido para PID %d, TID %d: Tamaño = %d bytes\n", pid, tid, tamaño)

	return tamaño, contenido
}

func getCurrentTimestamp() int64 {
	return time.Now().Unix()
}

func mandarAlFS(nombre string, tamaño int, contenido []byte) (bool, error) {
	// Construir la URL del endpoint
	url := fmt.Sprintf("http://%s:%d/crearArchivoDump", config.IP_Filesystem, config.Port_Filesystem)

	// Estructura de la solicitud
	type peticion struct {
		Nombre    string `json:"nombre"`
		Tamaño    int    `json:"tamaño"`
		Contenido string `json:"contenido"` // Codificado como Base64
	}

	// Crear la petición con el contenido codificado en Base64
	peticionFS := peticion{
		Nombre:    nombre,
		Tamaño:    tamaño,
		Contenido: base64.StdEncoding.EncodeToString(contenido),
	}

	// Convertir la estructura a JSON
	jsonBytes, err := json.Marshal(peticionFS)
	if err != nil {
		return false, fmt.Errorf("error al convertir el struct a JSON: %v", err)
	}

	// Crear la solicitud POST
	req, err := http.NewRequest("POST", url, bytes.NewBuffer(jsonBytes))
	if err != nil {
		return false, fmt.Errorf("error al crear la request: %v", err)
	}

	// Establecer el Content-Type como JSON
	req.Header.Set("Content-Type", "application/json")

	// Crear el cliente HTTP con timeout
	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return false, fmt.Errorf("error al enviar la request: %v", err)
	}
	defer resp.Body.Close()

	// Verificar el estado HTTP
	if resp.StatusCode != http.StatusOK {
		return false, fmt.Errorf("error en la respuesta del servidor: %v", resp.Status)
	}

	// Decodificar la respuesta JSON del servidor
	var response struct {
		Status  string `json:"status"`
		Message string `json:"message"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&response); err != nil {
		return false, fmt.Errorf("error al decodificar la respuesta del servidor: %v", err)
	}

	// Validar la respuesta del servidor
	if response.Status != "OK" {
		return false, fmt.Errorf("error en la respuesta del servidor: %s", response.Message)
	}

	return true, nil
}

func obtenerContextoEjecucion(w http.ResponseWriter, r *http.Request) {
	defer r.Body.Close()
	var respuesta utils.ContextoEjeccion

	type RequestBody struct {
		PID int `json:"pid"`
		TID int `json:"tid"`
	}

	// Leer el cuerpo de la solicitud
	var request RequestBody
	if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	// Validar PID y TID
	if request.PID < 0 || request.TID < 0 {
		http.Error(w, "Invalid PID or TID", http.StatusBadRequest)
		return
	}

	// Buscar el contexto de ejecución
	hilos, ok := mapaRegistrosHilos[request.PID]
	if !ok {
		http.Error(w, fmt.Sprintf("PID %d not found", request.PID), http.StatusNotFound)
		return
	}

	for _, registro := range hilos {
		if registro.TID == request.TID {
			// Construir respuesta
			respuesta = utils.ContextoEjeccion{
				RegistroHilos: registro.RegistroHilos,
				Base:          mapaContextosProcesos[request.PID].Base,
				Limit:         mapaContextosProcesos[request.PID].Limit,
			}

			// Responder en JSON
			w.Header().Set("Content-Type", "application/json")
			if err := json.NewEncoder(w).Encode(respuesta); err != nil {
				http.Error(w, "Error encoding response", http.StatusInternalServerError)
				return
			}

			Logger.Info("## Contexto Solicitado - (PID:TID) - (%d:%d)\n", request.PID, request.TID)
			return
		}
	}
	aplicarRetardo()

	http.Error(w, fmt.Sprintf("Thread with TID %d not found in PID %d", request.TID, request.PID), http.StatusNotFound)
}

func actualizarContextoEjecucion(w http.ResponseWriter, r *http.Request) {
	defer r.Body.Close()

	type RequestBody struct {
		PID           int                 `json:"pid"`
		TID           int                 `json:"tid"`
		RegistroHilos utils.RegistroHilos `json:"registroHilos"`
	}
	// Leer el cuerpo de la solicitud
	var request RequestBody
	err := json.NewDecoder(r.Body).Decode(&request)
	if err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	pid := request.PID
	tid := request.TID
	registroNuevo := request.RegistroHilos

	// Buscar el hilo en los registros del mapa
	hilos, ok := mapaRegistrosHilos[pid]
	if !ok {
		http.Error(w, fmt.Sprintf("PID %d not found", pid), http.StatusNotFound)
		return
	}
	aplicarRetardo()

	for i, hilo := range hilos {
		if hilo.TID == tid {
			// Actualizar registros del hilo
			hilos[i].RegistroHilos = registroNuevo
			mapaRegistrosHilos[pid] = hilos

			w.WriteHeader(http.StatusOK)
			w.Write([]byte("Contexto actualizado OK"))
			Logger.Info("## Contexto Actualizado - (PID:TID) - (%d:%d)\n", pid, tid)
			return
		}

	}

	http.Error(w, fmt.Sprintf("Thread with TID %d not found in PID %d", pid, tid), http.StatusNotFound)
}

func obtenerInstruccion(w http.ResponseWriter, r *http.Request) {
	// Leer el PID, TID y el Program Counter (PC) desde los parámetros de la URL
	type RequestBody struct {
		PID int `json:"pid"`
		TID int `json:"tid"`
		PC  int `json:"pc"`
	}

	var request RequestBody
	err := json.NewDecoder(r.Body).Decode(&request)
	if err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	pid := request.PID
	tid := request.TID
	pc := request.PC

	// Obtener las instrucciones del mapa
	if instrucciones, ok := mapaInstrucciones[tid]; ok {
		// Comprobar si el Program Counter (PC) está dentro de los límites
		if pc < 0 || pc >= len(instrucciones) {
			http.Error(w, "PC fuera de rango", http.StatusBadRequest)
			return
		}

		// Obtener la instrucción correspondiente
		instruccion := instrucciones[pc]
		aplicarRetardo()
		// Devolver la instrucción en formato JSON
		Logger.Info("## Obtener instrucción - (PID:TID) - (%d:%d) - Instrucción: %s %s\n", pid, tid, instruccion)
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{"instruccion": instruccion})
		return

	}

	http.Error(w, "PID no encontrado", http.StatusNotFound)
}

func readMem(w http.ResponseWriter, r *http.Request) {

	// Estructura para recibir los datos de la solicitud
	type RequestBody struct {
		DireccionFisica int `json:"direccionFisica"+` // Dirección inicial para leer
	}

	// Decodificar el cuerpo de la solicitud
	var request RequestBody
	err := json.NewDecoder(r.Body).Decode(&request)
	if err != nil {
		http.Error(w, "Cuerpo de solicitud inválido", http.StatusBadRequest)
		return
	}

	direccion := request.DireccionFisica

	// Validar que la dirección esté dentro del rango de la memoria
	if direccion < 0 || direccion+3 >= len(memoriaUsuario) {
		http.Error(w, "Dirección fuera de los límites de la memoria", http.StatusBadRequest)
		return
	}

	// Leer los 4 bytes desde la memoria
	datos := memoriaUsuario[direccion : direccion+4]
	aplicarRetardo()

	// Responder con los datos leídos
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string][]byte{"datos": datos})

	Logger.Info("Leyendo datos desde dirección %d: %v\n", direccion, datos)
}

func writeMem(w http.ResponseWriter, r *http.Request) {
	defer r.Body.Close()

	// Estructura para recibir los datos de la solicitud
	type RequestBody struct {
		DireccionFisica int    `json:"direccionFisica"` // Dirección inicial donde escribir
		Datos           []byte `json:"datos"`           // Array con los bytes a escribir (esperamos 4 bytes)
	}

	// Decodificar el cuerpo de la solicitud
	var request RequestBody
	err := json.NewDecoder(r.Body).Decode(&request)
	if err != nil {
		http.Error(w, "Cuerpo de solicitud inválido", http.StatusBadRequest)
		return
	}

	// Validar tamaño de los datos
	if len(request.Datos) != 4 {
		http.Error(w, "Debe enviar exactamente 4 bytes", http.StatusBadRequest)
		return
	}

	direccion := request.DireccionFisica
	datos := request.Datos

	// Validar que la dirección física esté dentro del rango de la memoria
	if direccion < 0 {
		http.Error(w, "Dirección fuera de los límites de la memoria", http.StatusBadRequest)
		return
	}

	// Escribir los 4 bytes en la Memoria de Usuario
	for i := 0; i < 4; i++ {
		memoriaUsuario[direccion+i] = datos[i]
	}
	aplicarRetardo()

	// Responder OK
	w.WriteHeader(http.StatusOK)
	w.Write([]byte("OK"))
	Logger.Info("Escribiendo datos %v en dirección %d\n", datos, direccion)
}

func aplicarRetardo() {
	time.Sleep(time.Duration(config.Response_Delay)) // Sin multiplicar por time.Millisecond
}
