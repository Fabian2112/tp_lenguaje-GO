package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/ioutil"
	"log"
	"log/slog"
	"math"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"kernel/utils"
)

// TIPOS
type mutex sync.Mutex
type NamedMutex struct {
	Nombre  string
	Mutex   sync.Mutex
	Status  string
	TIDCola []int
}
type ProcesoBody struct {
	PID int `json:"pid"`
}
type PCB struct {
	ID    int          `json:"ID"`
	TIDS  []int        `json:"TIDS"`
	Mutex []NamedMutex `json:"Mutexs del proceso"`
}
type TCB struct {
	PID         int          `json:"PID"`
	TID         int          `json:"TID"`
	Prioridad   int          `json:"Prioridad"`
	RutaArchivo string       `json:"RutaArchivo"`
	Mutex       []NamedMutex `json:"Mutexs del hilo"`
}

type Proceso struct {
	RutaArchivo string `json:"RutaArchivo"`
	SizeProceso int    `json:"SizeProceso"`
	Prioridad   int    `json:"Prioridad"`
}

type TCBJoined struct {
	HiloBloqueado     int `json:"HiloBloqueado"`
	HiloDebeFinalizar int `json:"HiloDebeFinalizar"`
}
type MutexName struct {
	Name string `json:"Name"`
}

//FIN TIPOS

// VARIABLES
var PCB_ID = -1                      //De aca va incrementando cada PCB
var kernel_config utils.KernelConfig //Config global del kernel
var cola_new []PCB
var cola_newAux []PCB
var cola_procesosToCreate []Proceso
var cola_ready []TCB
var cola_exit []TCB
var cola_exec []TCB
var cola_blocked []TCB
var PCBS []PCB
var colasMultinivel = make(map[int][]TCB) // Inicializamos correctamente el mapa
var hilosJoined []TCBJoined
var Logger *slog.Logger

//FIN VARIABLES

// MUTEX
var joined sync.Mutex
var pid_mutex sync.Mutex
var cola_new_mutex sync.Mutex
var cola_newAux_mutex sync.Mutex
var cola_ready_mutex sync.Mutex
var cola_exit_mutex sync.Mutex
var cola_exec_mutex sync.Mutex
var cola_blocked_mutex sync.Mutex
var list_PCBS sync.Mutex
var planificacionMutex sync.Mutex
var endpointCreateMutex sync.Mutex
var colasMultinivelMutex sync.Mutex
var planificador sync.Mutex
var intProcesada sync.Mutex

// FIN   MUTEEEX
// Canales
var nuevoElementoReadyDesalojo = make(chan bool)
var processEnded = make(chan bool)
var processInterrupted = make(chan bool)
var doneQuantum = make(chan bool)
var execEmpty = make(chan bool)

// Canal de señalización para indicar que la inicialización está completa
var initDone = make(chan struct{})

// Canal para notificar cuando se agrega un TCB a cola_exit
var tcbChannel = make(chan TCB)
var planificadorAct = true

func main() {
	fmt.Print("entro")
	kernel_config = *utils.IniciarConfiguracionKernel("utils/config.json") //Carga la configuracion del archivo config.json de la carpeta utils del kernel
	// Verificar si hay argumentos suficientes
	if len(os.Args) < 2 {
		fmt.Println("Uso: ./bin/kernel [archivo_pseudocodigo] [tamanio_proceso] [...args]")
		os.Exit(1)
	}
	// Capturar argumentos
	archivoPseudocodigo := os.Args[1]
	tamanioProcesoStr := os.Args[2]

	// Convertir tamaño de proceso a entero
	tamanioProceso, er := strconv.Atoi(tamanioProcesoStr)
	if er != nil {
		fmt.Printf("Error: %s no es un tamaño de proceso válido\n", tamanioProcesoStr)
		os.Exit(1)
	}

	go func() {
		// Inicialización de componentes
		planificacionCombinada()
		CreateProcesoHiloInicial(archivoPseudocodigo, tamanioProceso)
		fmt.Println("Cola ready inicial", cola_ready)

		// Señaliza que la inicialización está completa
		close(initDone)
	}()

	// Esperar a que la inicialización termine
	<-initDone
	fmt.Println("Cola ready inicial", cola_ready)
	//planification()

	mux := http.NewServeMux()
	go VerificarTCBEnCanal()
	mux.HandleFunc("PUT /createProcess", createProceso)
	mux.HandleFunc("POST /endProcess", endProceso)
	mux.HandleFunc("POST /compactarProceso", endProceso)
	mux.HandleFunc("PUT /thread_create", thread_create)
	mux.HandleFunc("POST /thread_cancel", thread_cancel)
	mux.HandleFunc("POST /thread_exit", THREAD_EXIT)
	mux.HandleFunc("POST /thread_join", thread_join)
	mux.HandleFunc("POST /mutexCreate", mutex_create)
	mux.HandleFunc("POST /mutexLock", mutex_lock)
	mux.HandleFunc("POST /mutexUnlock", mutex_unlock)
	mux.HandleFunc("POST /dumpMemory", dumpMemory)
	mux.HandleFunc("POST /IO", IO)
	mux.HandleFunc("POST /executionProcess", executionProces)
	mux.HandleFunc("GET /infoProceso/", getProceso)
	mux.HandleFunc("GET /colasMultinivel", getColasMultinivel)

	log.Println("El Kernel server está a la escucha en el puerto", fmt.Sprint(kernel_config.Port))
	err := http.ListenAndServe(fmt.Sprint(":", kernel_config.Port), mux)
	if err != nil {
		panic(err)
	}

}

func CreateProcesoHiloInicial(ruta string, size int) {
	procesToCreate := Proceso{
		RutaArchivo: ruta,
		SizeProceso: size,
		Prioridad:   0,
	}
	//Primero obtengo la info del proceso a crear
	//Crear PCB
	pid_mutex.Lock()
	PCB_ID += 1

	nuevoPCB := crearPCB(PCB_ID)
	pid_mutex.Unlock()

	response, err := SolicitarEspacioMemoriaProcesos(procesToCreate, nuevoPCB.ID)
	if err != nil {
		fmt.Printf("Error: %v\n", err)
	} else if response == "Compactar" {
		detenerPlanificacion()
	} else { //La conexión con la memoria es exitosa
		fmt.Printf("Respuesta memoria crear hilo: %s\n", response)
		crearHilo(&nuevoPCB, procesToCreate.Prioridad, procesToCreate.RutaArchivo)
		// Convertir el struct a JSON
		list_PCBS.Lock()
		PCBS = append(PCBS, nuevoPCB)
		list_PCBS.Unlock()

	}

}
func detenerPlanificacion() {
	planificador.Lock()
	planificadorAct = false
	planificador.Unlock()
	go monitorizarCola(execEmpty)

}
func monitorizarCola(notify <-chan bool) {
	for {
		// Espera la notificación de que la cola está vacía
		<-notify
		fmt.Println("ENTRO MONITORIZAR COMPACT")
		url := fmt.Sprintf("http://%s:%d/CompactarMemoria", kernel_config.IP_Memoria, kernel_config.Port_Memoria)

		// Crear la request POST con el JSON en el cuerpo
		req, _ := http.NewRequest("POST", url, nil)

		// Establecer el Content-Type como JSON
		req.Header.Set("Content-Type", "application/json")

		// Hacer la request
		client := &http.Client{}
		resp, err := client.Do(req)
		if err != nil {
			fmt.Errorf("error al enviar la request: %v", err)
		}
		defer resp.Body.Close()

		planificador.Lock()
		planificadorAct = true
		planificador.Unlock()
		fmt.Println("La cola_exec está vacía. Realizando acción...")
		if len(cola_new) > 0 {
			inicializarProceso(cola_new[0])
		} else {
			log.Println("Enviando mensaje al canal 'asignarNuevoProceso'...")
			processEnded <- true // Esto bloquea si no hay un receptor.
			log.Println("Mensaje enviado al canal 'asignarNuevoProceso'.")
			Logger.Info("“## (<PID>:0) Se crea el proceso - Estado: NEW")
		}
	}
}
func crearPCB(PID int) PCB { //Recibe el PID y crea el PCB con ese dato
	return PCB{
		ID:    PID,
		TIDS:  []int{},
		Mutex: []NamedMutex{},
	}

}

// Función para buscar el indice del PCB en base al PID
func buscarPCB(PID int) int {
	for i, pcbs := range PCBS {
		if pcbs.ID == PID {
			fmt.Println("ID del get proceso", i)
			return i // Devuelve el struct y true si se encuentra
		}
	}

	return -1 // Devuelve un struct vacío y false si no se encuentra
}

func crearHilo(PCBProcess *PCB, priority int, rutaArchivo string) TCB {

	//Traigo el PCB para calcular su ultimo indice y así ver cual es el proximo tid a asignar
	var nuevoTID int
	if len(PCBProcess.TIDS) == 0 {
		nuevoTID = 0
	} else {
		ultimoTID := len(PCBProcess.TIDS) - 1
		nuevoTID = PCBProcess.TIDS[ultimoTID] + 1 //Al ultimo TID le sumo uno
		fmt.Println("Numero TID", nuevoTID)
	}
	PCBProcess.TIDS = append(PCBProcess.TIDS, nuevoTID)
	Logger.Info("## (<PID>:<TID>) Se crea el Hilo - Estado: READY")
	fmt.Println("--------------------------------------------")
	nuevoHilo := TCB{
		PID:         PCBProcess.ID,
		TID:         nuevoTID,
		Prioridad:   priority,
		RutaArchivo: rutaArchivo,
		Mutex:       []NamedMutex{},
	}

	// Agrega el nuevo PCB a la cola de nuevos procesos
	cola_ready_mutex.Lock()

	cola_ready = append(cola_ready, nuevoHilo)

	cola_ready_mutex.Unlock()

	jsonData, err := json.Marshal(nuevoHilo)
	if err != nil {
		log.Fatal(err)
	}
	response, err := SolicitarEspacioHilos(nuevoHilo)
	if err != nil {
		fmt.Printf("Error: %v\n", err)

	} else { //La conexión con la memoria es exitosa
		fmt.Printf("Respuesta: %s\n", response)
	}
	agregarTCB(nuevoHilo.Prioridad, nuevoHilo)
	// Imprimimos el JSON como una cadena
	fmt.Println("Info hilo", string(jsonData))
	if len(cola_ready) == 1 && cola_ready[0].TID == 0 && cola_ready[0].PID == 0 { //Si el primer proceso llamo a ejecutar el primero
		log.Println("Enviando mensaje al canal 'asignarNuevoProceso'...")
		processEnded <- true // Esto bloquea si no hay un receptor.
		log.Println("Mensaje enviado al canal 'asignarNuevoProceso'.")
	} else if len(cola_exec) == 0 {
		log.Println("Enviando mensaje al canal 'asignarNuevoProceso'...")
		processEnded <- true // Esto bloquea si no hay un receptor.
		log.Println("Mensaje enviado al canal 'asignarNuevoProceso'.")
	} else {
		// Enviar mensaje al canal para que sea procesado
		log.Println("Enviando mensaje al canal 'nuevoElementoReadyDesalojo'...")
		nuevoElementoReadyDesalojo <- true // Esto bloquea si no hay un receptor.
		log.Println("Mensaje enviado al canal 'nuevoElementoReadyDesalojo'.")
	}

	return nuevoHilo
}

// Función que recibe un número, verifica si existe la clave, y si no, la crea
func agregarTCB(numero int, tcb TCB) {
	// Verificar si ya existe la clave
	if _, existe := colasMultinivel[numero]; existe {
		// Si ya existe, simplemente agregamos el TCB al arreglo
		colasMultinivel[numero] = append(colasMultinivel[numero], tcb)
	} else {
		// Si no existe, creamos una nueva entrada con el TCB
		colasMultinivel[numero] = []TCB{tcb}
	}
	fmt.Println("AGREGADO A COLAS", colasMultinivel)
}
func getProceso(w http.ResponseWriter, r *http.Request) {
	// Extraer el valor de la URL (por ejemplo: "getProceso/1")
	// La URL tiene la forma "/getProceso/{id}", y capturamos {id}
	// Usamos r.URL.Path para obtener la ruta completa
	fmt.Println("Cola exec", cola_exec)
	path := r.URL.Path

	// Partimos la ruta en partes usando "/"
	parts := strings.Split(path, "/")
	var PID int
	// El último valor (el ID) se encuentra en la última posición de `parts`
	if len(parts) > 0 {
		id := parts[len(parts)-1]
		PID, _ = strconv.Atoi(id)

	} else {
		http.Error(w, "ID no encontrado", http.StatusBadRequest)
	}
	pcbProceso := PCBS[PID]
	// Convertir el struct a JSON
	jsonData, err := json.Marshal(pcbProceso)
	if err != nil {
		fmt.Println("Error al convertir a JSON:", err)
		return
	}

	// Imprimir el JSON como string
	fmt.Println(string(jsonData))
	// Establecer el encabezado de tipo de contenido a JSON
	w.Header().Set("Content-Type", "application/json")
	// Codificar el objeto a JSON y escribirlo en la respuesta
	json.NewEncoder(w).Encode(pcbProceso)
	w.WriteHeader(http.StatusOK)

}
func getColasMultinivel(w http.ResponseWriter, r *http.Request) {
	// Convertir el struct a JSON
	jsonData, err := json.Marshal(colasMultinivel)
	if err != nil {
		fmt.Println("Error al convertir a JSON:", err)
		return
	}

	// Imprimir el JSON como string
	fmt.Println(string(jsonData))
	// Establecer el encabezado de tipo de contenido a JSON
	w.Header().Set("Content-Type", "application/json")
	// Codificar el objeto a JSON y escribirlo en la respuesta
	json.NewEncoder(w).Encode(colasMultinivel)
	w.WriteHeader(http.StatusOK)

}

func inicializarProceso(nuevoPCB PCB) (PCB, error) {
	//fmt.Println("COLA NEW AL PPIO", cola_new)
	procesToCreate := cola_procesosToCreate[0]

	// Solicitar espacio en memoria
	respuesta, _ := SolicitarEspacioMemoriaProcesos(procesToCreate, nuevoPCB.ID)
	//fmt.Println("-- Cola procesos a crear ini", cola_newAux)
	//fmt.Println("-- Cola procesos a crear info ini", cola_procesosToCreate)
	fmt.Println("RESP MEMORIA", respuesta)
	if respuesta == "Compactar" {
		detenerPlanificacion()
		if len(cola_new) > 0 && cola_new[0].ID == nuevoPCB.ID {
			//fmt.Println("Cola new al terminar de inicializar", cola_new)
			return nuevoPCB, errors.New("No se pudo inicializar, sigue encolado el proceso")
		} else {
			// Agregar el nuevo PCB a la cola de nuevos procesos
			cola_new_mutex.Lock()
			cola_new = append(cola_new, nuevoPCB)
			cola_new_mutex.Unlock()
			//fmt.Println("Cola new al terminar de inicializar", cola_new)
			//fmt.Println("-- Cola procesos a crear fin", cola_newAux)
			//fmt.Println("-- Cola procesos a crear info fin", cola_procesosToCreate)
			return nuevoPCB, errors.New("No se pudo inicializar, agregado a new")
		}
	} else if respuesta != "Proceso creado OK" {
		if len(cola_new) > 0 && cola_new[0].ID == nuevoPCB.ID {
			//fmt.Println("Cola new al terminar de inicializar", cola_new)
			return nuevoPCB, errors.New("No se pudo inicializar, sigue encolado el proceso")
		} else {
			// Agregar el nuevo PCB a la cola de nuevos procesos
			cola_new_mutex.Lock()
			cola_new = append(cola_new, nuevoPCB)
			cola_new_mutex.Unlock()
			//fmt.Println("Cola new al terminar de inicializar", cola_new)
			//fmt.Println("-- Cola procesos a crear fin", cola_newAux)
			//fmt.Println("-- Cola procesos a crear info fin", cola_procesosToCreate)
			return nuevoPCB, errors.New("No se pudo inicializar, agregado a new")
		}

	} else { // La conexión con la memoria fue exitosa
		//fmt.Printf("Respuesta: %s\n", response)

		// Crear hilo para el proceso y cambiar a estado READY
		crearHilo(&nuevoPCB, procesToCreate.Prioridad, procesToCreate.RutaArchivo)
		// Buscar el elemento y actualizarlo
		for i, PCB := range PCBS {
			if PCB.ID == nuevoPCB.ID {
				PCBS[i] = nuevoPCB
				break
			}
		}

		cola_new_mutex.Lock()
		cola_new = []PCB{}
		cola_new_mutex.Unlock()

		// Log obligatorio
		// Gestionar cola_procesosToCreate
		if len(cola_procesosToCreate) > 1 {
			cola_procesosToCreate = cola_procesosToCreate[1:]
			//fmt.Println("-- Cola procesos a crear info fin", cola_procesosToCreate)
		} else {
			cola_procesosToCreate = []Proceso{}
		}

		// Verificar si hay procesos esperando en cola_newAux
		if len(cola_newAux) > 0 {
			nuevoProceso := cola_newAux[0]
			cola_newAux = cola_newAux[1:] // Remover el proceso de la cola
			inicializarProceso(nuevoProceso)
		}
		//fmt.Println("-- Cola procesos a crear fin", cola_newAux)
		//fmt.Println("-- Cola procesos a crear info fin", cola_procesosToCreate)
		log.Printf("Se crea el proceso %d en NEW", nuevoPCB.ID)
		//fmt.Println("--------------------------------------------")
		return nuevoPCB, nil
	}
}
func createProceso(w http.ResponseWriter, r *http.Request) {
	fmt.Println("COLA NEW AL PPIO", cola_new)
	// Bloquear el acceso al endpoint
	endpointCreateMutex.Lock()
	defer endpointCreateMutex.Unlock() // Se asegura de que el Unlock se llame al final de la función
	//Primero obtengo la info del proceso a crear
	var procesToCreate Proceso
	err := json.NewDecoder(r.Body).Decode(&procesToCreate)
	if err != nil {
		http.Error(w, "Error al decodificar JSON", http.StatusBadRequest)
		return
	}
	cola_procesosToCreate = append(cola_procesosToCreate, procesToCreate)
	//Crear PCB
	pid_mutex.Lock()
	PCB_ID += 1
	nuevoPCB := crearPCB(PCB_ID)
	pid_mutex.Unlock()

	fmt.Println("Cola new inicial", cola_new)
	if len(cola_new) == 0 {
		nuevoPCB, error := inicializarProceso(nuevoPCB)
		fmt.Println("Nuevo PCB", nuevoPCB)
		if error != nil {
			fmt.Printf("Error: %v\n", err)
			http.Error(w, "No se pudo inicializar el proceso no hay espacio", http.StatusInternalServerError)
		} else {
			// Establecer el encabezado de tipo de contenido a JSON
			w.Header().Set("Content-Type", "application/json")
			// Codificar el objeto a JSON y escribirlo en la respuesta
			json.NewEncoder(w).Encode(nuevoPCB)
			w.WriteHeader(http.StatusOK)
		}
		list_PCBS.Lock()
		PCBS = append(PCBS, nuevoPCB)
		list_PCBS.Unlock()
	} else {
		cola_newAux_mutex.Lock()
		cola_newAux = append(cola_newAux, nuevoPCB)
		cola_newAux_mutex.Unlock()
		list_PCBS.Lock()
		PCBS = append(PCBS, nuevoPCB)
		list_PCBS.Unlock()
		fmt.Print("Cola new aux al crear", cola_newAux)
		fmt.Print("Cola procesos", cola_procesosToCreate)
		// Establecer el encabezado de tipo de contenido a JSON
		w.Header().Set("Content-Type", "application/json")
		// Codificar el objeto a JSON y escribirlo en la respuesta
		json.NewEncoder(w).Encode("Proceso encolado para ser creao al vaciarse la cola new")
		w.WriteHeader(http.StatusOK)
	}

}

// Solicita creación de espacio en Memoria para procesos
func SolicitarEspacioMemoriaProcesos(procesToCreate Proceso, PID int) (string, error) { //-depurado-
	// Crear la URL con la dirección IP y el puerto
	url := fmt.Sprintf("http://%s:%d/createProceso", kernel_config.IP_Memoria, kernel_config.Port_Memoria)
	type BodyRequest struct {
		PID  int `json:"pid"`
		Size int `json:"size"`
	}
	body := BodyRequest{
		PID:  PID,
		Size: procesToCreate.SizeProceso,
	}
	jsonBytes, err := json.Marshal(body)
	if err != nil {
		return "", fmt.Errorf("error al convertir el struct a JSON: %v", err)
	}

	// Crear la request POST con el JSON en el cuerpo
	req, err := http.NewRequest("POST", url, bytes.NewBuffer(jsonBytes))
	if err != nil {
		return "", fmt.Errorf("error al crear la request: %v", err)
	}

	// Establecer el Content-Type como JSON
	req.Header.Set("Content-Type", "application/json")

	// Hacer la request
	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("error al enviar la request: %v", err)
	}
	defer resp.Body.Close()

	// Leer la respuesta
	respBody, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("error al leer la respuesta: %v", err)
	}

	// Devolver la respuesta como string
	return string(respBody), nil
}
func SolicitarEspacioHilos(hiloTCB TCB) (string, error) { //-depurado-
	// Crear la URL con la dirección IP y el puerto
	url := fmt.Sprintf("http://%s:%d/crearHilo", kernel_config.IP_Memoria, kernel_config.Port_Memoria)

	type RequestBody struct {
		PID  int    `json:"pid"`
		TID  int    `json:"tid"` // El PID y TID que se enviarán en el cuerpo
		FILE string `json:"file"`
	}
	body := RequestBody{
		PID:  hiloTCB.PID,
		TID:  hiloTCB.TID,
		FILE: hiloTCB.RutaArchivo,
	}
	jsonBytes, err := json.Marshal(body)
	if err != nil {
		return "", fmt.Errorf("error al convertir el struct a JSON: %v", err)
	}

	// Crear la request POST con el JSON en el cuerpo
	req, err := http.NewRequest("POST", url, bytes.NewBuffer(jsonBytes))
	if err != nil {
		return "", fmt.Errorf("error al crear la request: %v", err)
	}

	// Establecer el Content-Type como JSON
	req.Header.Set("Content-Type", "application/json")

	// Hacer la request
	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("error al enviar la request: %v", err)
	}
	defer resp.Body.Close()

	// Leer la respuesta
	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("error al leer la respuesta: %v", err)
	}

	// Devolver la respuesta como string
	return string(respBody), nil
}
func mandarHilosExitProceso(PID int) error {

	cola_ready_mutex.Lock()
	// Slice donde vamos a almacenar los elementos eliminados

	// Recorrer el slice de personas
	for i := 0; i < len(cola_ready); i++ {
		tcbInicial := cola_ready[i]
		if tcbInicial.PID == PID {
			// Si el ID coincide, agregar a la lista de eliminadas
			cola_exit_mutex.Lock()
			cola_exit = append(cola_exit, tcbInicial)
			cola_ready = append(cola_ready[:i], cola_ready[i+1:]...) // Eliminar el elemento de cola_ready
			cola_exit_mutex.Unlock()

			// Decrementar el índice para no saltarse el siguiente elemento
			i--
		}
	}

	cola_ready_mutex.Unlock()
	colasMultinivelMutex.Lock()
	for prioridad, lista := range colasMultinivel {
		i := 0

		// Filtrar en el lugar
		for _, tcb := range lista {
			if tcb.PID != PID {
				lista[i] = tcb
				i++
			}
		}

		// Actualizar la lista o eliminar la clave
		if i == 0 {
			delete(colasMultinivel, prioridad) // Elimina la clave si está vacía
		} else {
			colasMultinivel[prioridad] = lista[:i] // Acorta la lista
		}
	}
	colasMultinivelMutex.Unlock()
	//Sumo el tcb ejecutando a exit
	cola_exit_mutex.Lock()
	cola_exit = append(cola_exit, cola_exec[0])
	cola_exit_mutex.Unlock()
	fmt.Println("Cola ready antes de sacar exit", cola_ready)
	fmt.Println("cola exit dsp de fin", cola_exit)
	fmt.Println("cola ready dsp fin", cola_ready)
	fmt.Println("cola MUTLI dsp fin", colasMultinivel)
	//Desalojo el proceso de exec

	//Vacia la cola
	return nil
}

func finalizarProceso(PIDEliminate int, isExec bool) string {
	fmt.Println("Cola exec inicio de endProceso", cola_exec)
	Logger.Info(fmt.Sprintf("## Finaliza el proceso %d", PIDEliminate))
	fmt.Println("cola ready al principio", cola_ready)
	//Tengo que notificar a la memoria para que libere el espacio del PID
	url := fmt.Sprintf("http://%s:%d/finalizarProceso", kernel_config.IP_Memoria, kernel_config.Port_Memoria)

	bodyRequst := ProcesoBody{PID: PIDEliminate}

	jsonBytes, _ := json.Marshal(bodyRequst)

	// Crear la request POST con el JSON en el cuerpo
	req, _ := http.NewRequest("POST", url, bytes.NewBuffer(jsonBytes))
	// Hacer la request
	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		error := "Error al contactar el Módulo Memoria"
		return error
	}
	defer req.Body.Close()

	// Lee y muestra la respuesta de memoria
	_, err = ioutil.ReadAll(resp.Body)
	if err != nil {

		error := "Error al leer la respuesta de Módulo B"
		return error
	}
	//Liberar el PCB
	list_PCBS.Lock()
	indicePCB := buscarPCB(PIDEliminate)

	PCBS = append(PCBS[:indicePCB], PCBS[indicePCB+1:]...)
	list_PCBS.Unlock()
	//Mandar hilos a EXIT
	//Necesito saber el PID del TCB para poder encontrar todos sus hilos
	PID := PIDEliminate
	errorHilos := mandarHilosExitProceso(PID)
	if errorHilos != nil {

		error := "Error al serializar datos"
		return error

	}

	if len(cola_new) > 0 {
		inicializarProceso(cola_new[0])
	}
	fmt.Println("Lista PCBs", PCBS)
	// Enviar mensaje al canal para que sea procesado
	if isExec {
		log.Println("Enviando mensaje al canal 'nuevoElementoReady'...")
		processEnded <- true // Esto bloquea si no hay un receptor.
		log.Println("Mensaje enviado al canal 'nuevoElementoReady'.")
	}
	return "Ok"
}
func endProceso(w http.ResponseWriter, r *http.Request) {
	fmt.Println("-------------------- Entro end proceso")
	err := finalizarProceso(cola_exec[0].PID, true)
	if err == "Error al contactar el Módulo Memoria" {
		http.Error(w, "Error al contactar el Módulo Memoria", http.StatusInternalServerError)
	}
	if err == "Ok" {
		msj := "Finalización de proceso exitosa"
		w.Header().Set("Content-Type", "application/json")
		// Codificar el objeto a JSON y escribirlo en la respuesta
		json.NewEncoder(w).Encode(msj)
		w.WriteHeader(http.StatusOK)
	}
}

func dumpMemory(w http.ResponseWriter, r *http.Request) {

	TCBEXEC := cola_exec[0]
	type ProcesoHilo struct {
		PID int `json:"PID"`
		TID int `json:"TID"`
	}

	log.Println("--------------------SAPE----------------------")

	PIDHilo := TCBEXEC.PID
	// Bloqueo de hilo invocado
	cola_blocked_mutex.Lock()
	cola_blocked = append(cola_blocked, TCBEXEC)
	cola_blocked_mutex.Unlock()
	fmt.Println("COLA READY PREVIO CANAL MEMORY", cola_ready)
	log.Println("Enviando mensaje al canal 'asignarNuevoProceso'...")

	processEnded <- true // Esto bloquea si no hay un receptor.
	log.Println("Mensaje enviado al canal 'asignarNuevoProceso'.")

	// Notificar a la memoria para que libere el espacio del PID
	url := fmt.Sprintf("http://%s:%d/memoryDump", kernel_config.IP_Memoria, kernel_config.Port_Memoria)
	bodyRequst := ProcesoHilo{PID: TCBEXEC.PID, TID: TCBEXEC.TID}

	jsonBytes, err := json.Marshal(bodyRequst)
	if err != nil {
		log.Printf("Error al marshallizar datos: %v", err)
		http.Error(w, "Error al procesar la solicitud", http.StatusInternalServerError)
		return
	}

	req, err := http.NewRequest("POST", url, bytes.NewBuffer(jsonBytes))
	if err != nil {
		log.Printf("Error al crear la solicitud: %v", err)
		http.Error(w, "Error interno del servidor", http.StatusInternalServerError)
		return
	}

	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		log.Printf("Error al enviar la solicitud: %v", err)
		http.Error(w, "Error al realizar la petición", http.StatusInternalServerError)
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusOK {
		log.Println("Respuesta de memoryDump recibida correctamente.")
		desbloquearHilo(&TCBEXEC, false)
		msj := "Dump memory realizado correctamente"
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(msj)
	} else {
		log.Printf("Error en memoryDump: StatusCode: %d", resp.StatusCode)
		http.Error(w, "Error en el memory Dump", http.StatusInternalServerError)
		finalizarProceso(PIDHilo, false)
	}
}

func IO(w http.ResponseWriter, r *http.Request) {
	segundosParam := r.URL.Query().Get("segundos")
	if segundosParam == "" {
		http.Error(w, "Segundos is missing", http.StatusBadRequest)
		return
	}
	sec, _ := strconv.Atoi(segundosParam)
	time.Sleep(time.Duration(sec) * time.Second)

	// Escribimos jsonData en la respuesta
	msj := "Proceso finalizó IO"
	if len(cola_exec) > 0 {
		Logger.Info(fmt.Sprintf("## (%d:%d) finalizó IO y pasa a READY", cola_exec[0].PID, cola_exec[0].TID))
	}
	w.Header().Set("Content-Type", "application/json")
	// Codificar el objeto a JSON y escribirlo en la respuesta
	json.NewEncoder(w).Encode(msj)
	w.WriteHeader(http.StatusOK)

}
func thread_create(w http.ResponseWriter, r *http.Request) {
	// Necesito saber que proceso esta en la cola de EXEC para tomar su pcb y así saberlo a que hilo agregar
	// por ahora tomo el pcb del primero proceso en la cola de new
	var hiloToCreate Proceso
	err := json.NewDecoder(r.Body).Decode(&hiloToCreate)
	if err != nil {
		http.Error(w, "Error al decodificar JSON", http.StatusBadRequest)
		return
	}
	TCBExecuting := cola_exec[0]
	PID_TCB := TCBExecuting.PID
	fmt.Println("Llego")
	hiloCreated := crearHilo(&PCBS[PID_TCB], hiloToCreate.Prioridad, hiloToCreate.RutaArchivo)
	w.Header().Set("Content-Type", "application/json")
	// Codificar el objeto a JSON y escribirlo en la respuesta
	json.NewEncoder(w).Encode(hiloCreated)
	w.WriteHeader(http.StatusOK)
}
func agregarMutexPCB(PCBProcess *PCB, nombreMutex string) error {
	// Inicializar la lista si es nil
	if PCBProcess.Mutex == nil {
		PCBProcess.Mutex = make([]NamedMutex, 0)
	}

	// Verificar si el mutex ya existe
	for _, m := range PCBProcess.Mutex {
		if m.Nombre == nombreMutex {
			fmt.Printf("Mutex '%s' ya existe en el PCB ID %d\n", nombreMutex, PCBProcess.ID)
			return nil
		}
	}

	// Agregar el nuevo mutex
	nuevoMutex := NamedMutex{
		Nombre: nombreMutex,
		Status: "Libre",
	}
	list_PCBS.Lock()
	PCBProcess.Mutex = append(PCBProcess.Mutex, nuevoMutex)
	list_PCBS.Unlock()
	fmt.Printf("Mutex '%s' creado y agregado al PCB ID %d\n", nombreMutex, PCBProcess.ID)

	return nil
}
func mutex_create(w http.ResponseWriter, r *http.Request) {
	// Necesito saber que proceso esta en la cola de EXEC para tomar su pcb y así saberlo a que hilo agregar
	// por ahora tomo el pcb del primero proceso en la cola de new

	var mutexToCreate MutexName
	err := json.NewDecoder(r.Body).Decode(&mutexToCreate)
	if err != nil {
		http.Error(w, "Error al decodificar JSON", http.StatusBadRequest)
		return
	}
	nombreMutex := mutexToCreate.Name
	TCBExecuting := cola_exec[0]
	PID_TCB := TCBExecuting.PID
	agregarMutexPCB(&PCBS[PID_TCB], nombreMutex)
	w.Header().Set("Content-Type", "application/json")
	// Codificar el objeto a JSON y escribirlo en la respuesta
	json.NewEncoder(w).Encode("Recurso" + nombreMutex + "creado")
	w.WriteHeader(http.StatusOK)
}
func asignarHiloMutex(tcbExecuting *TCB, nombreMutex string, proceso *PCB) error {
	for i := range proceso.Mutex {
		if proceso.Mutex[i].Nombre == nombreMutex {
			proceso.Mutex[i].Mutex.Lock()
			fmt.Printf("Mutex '%s' bloqueado\n", nombreMutex)
			proceso.Mutex[i].Status = "Bloqueado"
			tcbExecuting.Mutex = append(tcbExecuting.Mutex, proceso.Mutex[i])
			// Realizar operaciones críticas aquí

			proceso.Mutex[i].Mutex.Unlock()
			fmt.Printf("Mutex '%s' desbloqueado\n", nombreMutex)
			break
		}
	}
	return nil
}

func bloquearHiloMutex(tcbExecuting *TCB, nombreMutex string, proceso *PCB) error {
	for i := range proceso.Mutex {
		if proceso.Mutex[i].Nombre == nombreMutex {
			proceso.Mutex[i].Mutex.Lock()
			fmt.Printf("Mutex '%s' bloqueado\n", nombreMutex)
			proceso.Mutex[i].TIDCola = append(proceso.Mutex[i].TIDCola, tcbExecuting.TID)
			proceso.Mutex[i].Mutex.Unlock()
			break
		}
	}
	return nil
}

// Función para eliminar un TCB de la lista de Mutex por el nombre
func EliminarTCBdeMutex(tcb *TCB, nombreMutex string) {
	// Recorremos todos los NamedMutex en el TCB
	// Recorremos la lista de NamedMutex en el TCB
	for i := 0; i < len(tcb.Mutex); i++ {
		if tcb.Mutex[i].Nombre == nombreMutex {
			// Eliminar el NamedMutex de la lista
			tcb.Mutex = append(tcb.Mutex[:i], tcb.Mutex[i+1:]...)
			fmt.Printf("Mutex '%s' eliminado del TCB con PID %d\n", nombreMutex, tcb.PID)
			return
		}
	}
	fmt.Printf("No se encontró el mutex '%s' en el TCB con PID %d\n", nombreMutex, tcb.PID)

}
func liberarHiloMutex(tcbExecuting *TCB, nombreMutex string, proceso *PCB) error {
	for i := range proceso.Mutex {
		if proceso.Mutex[i].Nombre == nombreMutex {
			proceso.Mutex[i].Mutex.Lock()

			proceso.Mutex[i].Status = "Libre"
			// Realizar operaciones críticas aquí
			EliminarTCBdeMutex(tcbExecuting, nombreMutex)
			proceso.Mutex[i].Mutex.Unlock()
			fmt.Printf("Mutex '%s' desbloqueado\n", nombreMutex)
			break
		}
	}

	return nil
}

func mutex_lock(w http.ResponseWriter, r *http.Request) {
	// Necesito saber que proceso esta en la cola de EXEC para tomar su pcb y así saberlo a que hilo agregar
	// por ahora tomo el pcb del primero proceso en la cola de new

	var mutexToCreate MutexName
	err := json.NewDecoder(r.Body).Decode(&mutexToCreate)
	if err != nil {
		http.Error(w, "Error al decodificar JSON", http.StatusBadRequest)
		return
	}
	nombreMutex := mutexToCreate.Name
	TCBExecuting := cola_exec[0]
	PID_TCB := TCBExecuting.PID
	var msj string
	for i := range PCBS[PID_TCB].Mutex {
		if PCBS[PID_TCB].Mutex[i].Nombre == nombreMutex && PCBS[PID_TCB].Mutex[i].Status == "Libre" {
			asignarHiloMutex(&cola_exec[0], nombreMutex, &PCBS[PID_TCB])
			msj = "Recurso " + nombreMutex + " asingado al hilo " + string(TCBExecuting.TID)
		} else if PCBS[PID_TCB].Mutex[i].Nombre == nombreMutex && PCBS[PID_TCB].Mutex[i].Status == "Bloqueado" {
			cola_blocked_mutex.Lock()
			cola_blocked = append(cola_blocked, TCBExecuting)
			cola_blocked_mutex.Unlock()
			bloquearHiloMutex(&cola_exec[0], nombreMutex, &PCBS[PID_TCB])
			Logger.Info(fmt.Sprintf("## (%d:%d) - Bloqueado por: MUTEX", TCBExecuting.PID, TCBExecuting.TID))
			log.Println("Enviando mensaje al canal 'asignarNuevoProceso'...")
			processEnded <- true // Esto bloquea si no hay un receptor.
			log.Println("Mensaje enviado al canal 'asignarNuevoProceso'.")
			msj = "Recurso " + nombreMutex + " está ocupado. Hilo" + string(TCBExecuting.TID) + " bloqueado."
		}
	}
	Logger.Info(fmt.Sprintf("## (%d:%d) - Solicitó syscall: MUTEX_LOCK (%s)", TCBExecuting.PID, TCBExecuting.TID, nombreMutex))
	fmt.Println("COLA BLOCK", cola_blocked)
	w.Header().Set("Content-Type", "application/json")
	// Codificar el objeto a JSON y escribirlo en la respuesta
	json.NewEncoder(w).Encode(msj)
	w.WriteHeader(http.StatusOK)
}

func mutex_unlock(w http.ResponseWriter, r *http.Request) {
	// Necesito saber que proceso esta en la cola de EXEC para tomar su pcb y así saberlo a que hilo agregar
	// por ahora tomo el pcb del primero proceso en la cola de new

	var mutexToCreate MutexName
	err := json.NewDecoder(r.Body).Decode(&mutexToCreate)
	if err != nil {
		http.Error(w, "Error al decodificar JSON", http.StatusBadRequest)
		return
	}
	nombreMutex := mutexToCreate.Name
	TCBExecuting := cola_exec[0]
	PID_TCB := TCBExecuting.PID
	Logger.Info(fmt.Sprintf("## (%d:%d) - Solicitó syscall: MUTEX_UNLOCK (%s)", TCBExecuting.PID, TCBExecuting.TID, nombreMutex))
	var msj string
	for i := range PCBS[PID_TCB].Mutex {
		if PCBS[PID_TCB].Mutex[i].Nombre == nombreMutex && TCBExecuting.Mutex[i].Nombre == nombreMutex {

			liberarHiloMutex(&cola_exec[0], nombreMutex, &PCBS[PID_TCB])
			msj = "Recurso " + nombreMutex + " asingado al hilo " + string(TCBExecuting.TID)

			if len(PCBS[PID_TCB].Mutex[i].TIDCola) > 0 {
				// Recorremos el slice TIDCola

				var tcbToLock TCB
				for j, hilo := range cola_blocked {
					if hilo.TID == PCBS[PID_TCB].Mutex[j].TIDCola[0] && PCBS[PID_TCB].ID == hilo.PID {
						tcbToLock = cola_blocked[j]

						break
					}
				}

				fmt.Println("HILO a desblo", tcbToLock)
				asignarHiloMutex(&tcbToLock, nombreMutex, &PCBS[PID_TCB])
				desbloquearHilo(&tcbToLock, false)
				for i := 0; i < len(PCBS[PID_TCB].Mutex[i].TIDCola); i++ {
					if PCBS[PID_TCB].Mutex[i].TIDCola[i] == tcbToLock.TID {
						// Eliminar el valor en el índice i
						PCBS[PID_TCB].Mutex[i].TIDCola = append(PCBS[PID_TCB].Mutex[i].TIDCola[:i], PCBS[PID_TCB].Mutex[i].TIDCola[i+1:]...)
						return
					}
				}
			}
		}
	}
	fmt.Println("COLA BLOCK", cola_blocked)
	w.Header().Set("Content-Type", "application/json")
	// Codificar el objeto a JSON y escribirlo en la respuesta
	json.NewEncoder(w).Encode(msj)
	w.WriteHeader(http.StatusOK)
}
func desbloquearHilo(hiloDesbloquear *TCB, beingEliminated bool) error {
	cola_blocked_mutex.Lock()
	for i := 0; i < len(cola_blocked); i++ {
		if cola_blocked[i].PID == hiloDesbloquear.PID && cola_blocked[i].TID == hiloDesbloquear.TID {
			// Eliminar el TCB con el PID y TID dados
			cola_blocked = append(cola_blocked[:i], cola_blocked[i+1:]...)
			fmt.Printf("Cola exit dsp desbloquear hilo", cola_blocked)
		}
	}
	cola_blocked_mutex.Unlock()
	if !beingEliminated {
		cola_ready_mutex.Lock()

		cola_ready = append(cola_ready, *hiloDesbloquear)
		colasMultinivelMutex.Lock()
		agregarTCB(hiloDesbloquear.Prioridad, *hiloDesbloquear)
		colasMultinivelMutex.Unlock()
		log.Println("Enviando mensaje al canal 'nuevoElementoReadyDesalojo'...")
		nuevoElementoReadyDesalojo <- true // Esto bloquea si no hay un receptor.
		log.Println("Mensaje enviado al canal 'nuevoElementoReadyDesalojo'.")
		cola_ready_mutex.Unlock()
	}
	return nil
}

func finalizarHilo(TID int, isExec bool) error { //IsExec dice si se esta ejecutando el hilo o no
	var tcbToEnd TCB
	type RequestBody struct {
		PID int `json:"pid"`
		TID int `json:"tid"`
	}

	cola_ready_mutex.Lock()
	if !isExec {
		for i, hilo := range cola_ready {
			if hilo.TID == TID && cola_exec[0].PID == hilo.PID {
				tcbToEnd = cola_ready[i]
				break
			}
		}
	} else {
		tcbToEnd = cola_exec[0]
	}
	fmt.Println("TCB ELEGIDO", tcbToEnd)
	cola_ready_mutex.Unlock()
	Logger.Info(fmt.Sprintf("## (%d:%d) Finaliza el hilo", tcbToEnd.PID, tcbToEnd.TID))
	/*Al momento de finalizar un hilo, el Kernel deberá informar a la Memoria la finalización del mismo y
	deberá mover al estado READY a todos los hilos que se encontraban bloqueados por ese TID.
	De esta manera, se desbloquean aquellos hilos bloqueados por THREAD_JOIN y por mutex tomados por el hilo
	 finalizado (en caso que hubiera).*/
	//Puede que el TID este en exit o en ready
	//Tengo que notificar a la memoria para que libere el espacio del TID

	url := fmt.Sprintf("http://%s:%d/endHilo", kernel_config.IP_Memoria, kernel_config.Port_Memoria)
	var body = RequestBody{
		PID: tcbToEnd.PID,
		TID: tcbToEnd.TID,
	}
	jsonBytes, _ := json.Marshal(body)

	// Crear la request POST con el JSON en el cuerpo
	req, err := http.NewRequest("POST", url, bytes.NewBuffer(jsonBytes))
	if err != nil {
		fmt.Println("error al crear la request: %v", err)
	}

	// Establecer el Content-Type como JSON
	req.Header.Set("Content-Type", "application/json")

	// Hacer la request
	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		fmt.Println("error al enviar la request: %v", err)
	}
	defer resp.Body.Close()

	// Leer la respuesta
	res, er := ioutil.ReadAll(resp.Body)
	if er != nil {
		fmt.Println("error al leer la respuesta: %v", err)
	}
	fmt.Println("RESP Memory fin hilo", res)
	if !isExec {
		cola_ready_mutex.Lock()
		newColaReady := make([]TCB, 0, len(cola_ready))
		for i := 0; i < len(cola_ready); i++ {
			tcbs := cola_ready[i]
			if tcbs.TID != TID {
				newColaReady = append(newColaReady, tcbs)
			} else {
				cola_exit_mutex.Lock()
				cola_exit = append(cola_exit, tcbs)
				cola_exit_mutex.Unlock()
			}
		}
		cola_ready = newColaReady // Reemplazamos cola_ready con la versión filtrada
		cola_ready_mutex.Unlock()
		colasMultinivelMutex.Lock()
		for prioridad, lista := range colasMultinivel {
			i := 0

			// Filtrar en el lugar
			for _, tcb := range lista {
				if (tcb.PID != tcbToEnd.PID && tcb.TID != tcbToEnd.TID) || (tcb.PID == tcbToEnd.PID && tcb.TID != tcbToEnd.TID) {
					lista[i] = tcb
					i++
				}
			}

			// Actualizar la lista o eliminar la clave
			if i == 0 {
				delete(colasMultinivel, prioridad) // Elimina la clave si está vacía
			} else {
				colasMultinivel[prioridad] = lista[:i] // Acorta la lista
			}
		}
		colasMultinivelMutex.Unlock()
		fmt.Println("LLego al fin de endHilo en no exec")
	}
	fmt.Println("COLA READY DSP FINALIZAR HILO", cola_ready)
	fmt.Println("COLAS multinivel DSP FINALIZAR HILO", colasMultinivel)
	if isExec {
		cola_exit_mutex.Lock()
		cola_exit = append(cola_exit, cola_exec[0])
		cola_exit_mutex.Unlock()
		log.Println("Enviando mensaje al canal 'asignarNuevoProceso'...")
		processEnded <- true // Esto bloquea si no hay un receptor.
		log.Println("Mensaje enviado al canal 'asignarNuevoProceso'.")

	}
	fmt.Println("LLego al fin de endHilo")
	if len(tcbToEnd.Mutex) > 0 {
		for i := 0; i < len(tcbToEnd.Mutex); i++ {
			PID_TCB := tcbToEnd.PID
			nombreMutex := tcbToEnd.Mutex[i].Nombre
			liberarHiloMutex(&tcbToEnd, nombreMutex, &PCBS[PID_TCB])

			if len(PCBS[PID_TCB].Mutex[i].TIDCola) > 0 {
				// Recorremos el slice TIDCola

				var tcbToLock TCB
				for j, hilo := range cola_blocked {
					if hilo.TID == PCBS[PID_TCB].Mutex[j].TIDCola[0] && PCBS[PID_TCB].ID == hilo.PID {
						tcbToLock = cola_blocked[j]

						break
					}
				}

				fmt.Println("HILO a desblo", tcbToLock)
				asignarHiloMutex(&tcbToLock, nombreMutex, &PCBS[PID_TCB])
				desbloquearHilo(&tcbToLock, false)
				for j := 0; j < len(PCBS[PID_TCB].Mutex[i].TIDCola); j++ {
					if PCBS[PID_TCB].Mutex[j].TIDCola[j] == tcbToLock.TID {
						// Eliminar el valor en el índice i
						PCBS[PID_TCB].Mutex[j].TIDCola = append(PCBS[PID_TCB].Mutex[j].TIDCola[:j], PCBS[PID_TCB].Mutex[j].TIDCola[j+1:]...)

					}
				}
			}

		}
	}
	// Enviar el TCB al canal para su verificación
	tcbChannel <- tcbToEnd
	return nil

}

func VerificarTCBEnCanal() {
	for {
		select {
		case tcb := <-tcbChannel:
			if len(hilosJoined) > 1 {
				// Iteramos sobre el array de hilos y verificamos si el TCB.ID coincide con HiloDebeFinalizar
				for _, hilo := range hilosJoined {
					if tcb.TID == hilo.HiloDebeFinalizar {
						cola_blocked_mutex.Lock()
						for _, tcb := range cola_blocked {
							if tcb.TID == hilo.HiloBloqueado {
								desbloquearHilo(&tcb, false) // Retorna el TCB si lo encuentra y un true
							}
						}

						cola_blocked_mutex.Unlock()
						break // Si encontramos una coincidencia, salimos del bucle
					}
				}
			}
		}
	}
}
func thread_cancel(w http.ResponseWriter, r *http.Request) { //Recibe el TID
	tidParam := r.URL.Query().Get("tid")
	if tidParam == "" {
		http.Error(w, "TID is missing", http.StatusBadRequest)
		return
	}
	tid, _ := strconv.Atoi(tidParam)
	finalizarHilo(tid, false)
	fmt.Println("end hilo OK")

	jsonData, err := json.Marshal(cola_ready)
	if err != nil {
		http.Error(w, "Error al serializar datos", http.StatusInternalServerError)
		return
	}

	// Escribimos jsonData en la respuesta
	w.Header().Set("Content-Type", "application/json")
	w.Write(jsonData)

}
func thread_join(w http.ResponseWriter, r *http.Request) { //Recibe el TID

	tidParam := r.URL.Query().Get("tid")
	if tidParam == "" {
		http.Error(w, "TID is missing", http.StatusBadRequest)
		return
	}
	tid, _ := strconv.Atoi(tidParam)
	tcbExecuting := cola_exec[0]
	joined.Lock()
	hilosJoin := TCBJoined{
		HiloBloqueado:     tcbExecuting.TID,
		HiloDebeFinalizar: tid,
	}
	hilosJoined = append(hilosJoined, hilosJoin)
	joined.Unlock()
	cola_blocked_mutex.Lock()
	cola_blocked = append(cola_blocked, tcbExecuting)
	cola_blocked_mutex.Unlock()
	cola_exec_mutex.Lock()
	cola_exec = []TCB{}
	cola_exec_mutex.Unlock()
	Logger.Info(fmt.Sprintf("## (%d:%d) - Solicitó syscall: THREAD_JOIN", tcbExecuting.PID, tcbExecuting.TID))
	log.Println("----- JOIIIIN")
	log.Println("Enviando mensaje al canal 'asignarNuevoProceso'...")
	processInterrupted <- true // Esto bloquea si no hay un receptor.
	log.Println("Mensaje enviado al canal 'asignarNuevoProceso'.")

	// Escribimos jsonData en la respuesta
	w.Header().Set("Content-Type", "application/json")
	w.Write([]byte("Hilo bloqueado"))
	Logger.Info(fmt.Sprintf("## (%d:%d) - Bloqueado por: PTHREAD_JOIN", tcbExecuting.PID, tcbExecuting.TID))

}
func THREAD_EXIT(w http.ResponseWriter, r *http.Request) { //Elimina el que esta en exec
	tcbExecuting := cola_exec[0]
	eliminarHilo := finalizarHilo(tcbExecuting.TID, true)
	fmt.Println("end hilo", eliminarHilo)
	if eliminarHilo == nil {

		jsonData, err := json.Marshal(cola_ready)
		if err != nil {
			http.Error(w, "Error al serializar datos", http.StatusInternalServerError)
			return
		}

		// Escribimos jsonData en la respuesta
		w.Header().Set("Content-Type", "application/json")
		w.Write(jsonData)
	}

}

func planificarFifo() TCB {
	cola_ready_mutex.Lock()
	var tcbExecute TCB
	if len(cola_ready) > 0 && planificadorAct {
		//saco el TCB de la cola de readyys
		tcbExecute = cola_ready[0]
		if planificadorAct && len(cola_ready) > 0 {
			cola_ready = cola_ready[1:]
		}
		cola_ready_mutex.Unlock()
	} else {
		tcbExecute = TCB{}
	}
	return tcbExecute
}

func planificarPrioridades() TCB {
	cola_ready_mutex.Lock()
	defer cola_ready_mutex.Unlock()

	// Si no hay procesos en cola_ready, retornar un TCB vacío
	if len(cola_ready) == 0 {
		return TCB{}
	}

	// Seleccionar el proceso con la menor prioridad
	menor := cola_ready[0]
	indiceMenor := 0
	for index, tcb := range cola_ready[1:] {
		if tcb.Prioridad < menor.Prioridad {
			menor = tcb
			indiceMenor = index + 1
		}
	}

	// Si el proceso con menor prioridad ya está en ejecución, no hacemos nada
	if len(cola_exec) > 0 && menor.Prioridad >= cola_exec[0].Prioridad {
		// Retornamos el proceso en ejecución sin cambiar cola_ready
		return cola_exec[0]
	}
	if planificadorAct {
		// Eliminar el proceso seleccionado de cola_ready
		cola_ready = append(cola_ready[:indiceMenor], cola_ready[indiceMenor+1:]...)
	}
	// Devolver el proceso con la menor prioridad
	return menor
}

// Proceso simula la ejecución de un proceso
func manejarQuantum(TID int, PID int) {
	quantum := kernel_config.Quantum
	fmt.Printf("Hilo %d - Proceso %d: ejecutándose durante %d segundos...\n", TID, PID, quantum)

	// Simula el tiempo de ejecución
	time.Sleep(time.Duration(quantum) * time.Millisecond)
	// Notificar que el proceso terminó
	Logger.Info(fmt.Sprintf("## (%d:%d) - Desalojado por fin de Quantum", PID, TID))
	processInterrupted <- true
}
func obtenerPrimerElementoConClaveMenor(procesos map[int][]TCB) (int, *TCB) {
	var menorClave int = math.MaxInt // Usamos el máximo valor posible como punto de partida
	var elementoSeleccionado *TCB

	// Iteramos sobre las claves del mapa
	for clave, lista := range procesos {
		if clave < menorClave && len(lista) > 0 { // Validamos si es la menor clave y la lista no está vacía
			menorClave = clave
			elementoSeleccionado = &lista[0] // Seleccionamos el primer elemento de la lista
		}
	}
	fmt.Println("Elemento colas", elementoSeleccionado)
	return menorClave, elementoSeleccionado
}

func planificarColas() (TCB, int) {

	cola_ready_mutex.Lock()
	defer cola_ready_mutex.Unlock()

	// Si no hay procesos en cola_ready, retornar un TCB vacío
	if len(cola_ready) == 0 {
		return TCB{}, -100
	}

	colasMultinivelMutex.Lock()
	menorClave, tcbElegido := obtenerPrimerElementoConClaveMenor(colasMultinivel)
	colasMultinivelMutex.Unlock()

	//fmt.Println("Obtuvo elemento")
	// Si no hay un TCB seleccionado, retornar vacío
	if tcbElegido == nil {
		return TCB{}, -100
	}

	// Si el proceso con menor prioridad ya está en ejecución, no hacemos nada
	if len(cola_exec) > 0 && tcbElegido.Prioridad >= cola_exec[0].Prioridad {

		// Retornamos el proceso en ejecución sin cambiar cola_ready
		return cola_exec[0], -100
	}

	// Eliminar el proceso seleccionado de colasMultinivel
	if planificadorAct { //Lo elimino de ready si el plani esta activo
		colasMultinivelMutex.Lock()
		if lista, existe := colasMultinivel[menorClave]; existe && len(lista) > 0 {
			colasMultinivel[menorClave] = lista[1:] // Eliminar el primer elemento de la lista
			if len(colasMultinivel[menorClave]) == 0 {
				delete(colasMultinivel, menorClave) // Eliminar la clave si la lista queda vacía
			}
		}
		colasMultinivelMutex.Unlock()
		// Eliminar el proceso seleccionado de cola_ready

		for i := 0; i < len(cola_ready); {
			if cola_ready[i].PID == tcbElegido.PID && cola_ready[i].TID == tcbElegido.TID {
				// Eliminar elemento
				cola_ready = append(cola_ready[:i], cola_ready[i+1:]...)
			} else {
				i++ // Solo avanzar si no se elimina nada
			}
		}
	}
	// Devolver el proceso con la menor prioridad
	fmt.Println("TCB ELEGIDO COLAS", tcbElegido)
	return *tcbElegido, menorClave
}
func executeTCB(tcbExec TCB) (string, error) {
	//Saco el TCB de ready y lo mando a exec

	// Crear la URL con la dirección IP y el puerto
	url := fmt.Sprintf("http://%s:%d/execute", kernel_config.IP_CPU, kernel_config.Port_CPU)

	jsonBytes, err := json.Marshal(tcbExec)
	if err != nil {
		return "", fmt.Errorf("error al convertir el struct a JSON: %v", err)
	}

	// Crear la request POST con el JSON en el cuerpo
	req, err := http.NewRequest("PUT", url, bytes.NewBuffer(jsonBytes))
	if err != nil {
		return "", fmt.Errorf("error al crear la request: %v", err)
	}

	// Establecer el Content-Type como JSON
	req.Header.Set("Content-Type", "application/json")

	// Hacer la request
	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("error al enviar la request: %v", err)
	}
	defer resp.Body.Close()

	// Leer la respuesta
	respBody, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("error al leer la respuesta: %v", err)
	}
	fmt.Println("Respuesta CPU execute", resp.Body)
	// Devolver la respuesta como string
	return string(respBody), nil
}

func notifCPUInterruption(tcbExec TCB) (string, error) {
	//Saco el TCB de ready y lo mando a exec

	// Crear la URL con la dirección IP y el puerto
	url := fmt.Sprintf("http://%s:%d/interrupcion", kernel_config.IP_CPU, kernel_config.Port_CPU)

	type ProcessInterrupcion struct {
		PID     int    `json:"PID"`
		TID     int    `json:"TID"`
		Mensaje string `json:"Mensaje"`
	}
	var bodyRequest = ProcessInterrupcion{
		PID:     tcbExec.PID,
		TID:     tcbExec.TID,
		Mensaje: "Interrupción detectada",
	}
	jsonBytes, err := json.Marshal(bodyRequest)
	// Crear la request POST con el JSON en el cuerpo
	req, err := http.NewRequest("POST", url, bytes.NewBuffer(jsonBytes))
	if err != nil {
		return "", fmt.Errorf("error al crear la request: %v", err)
	}

	// Establecer el Content-Type como JSON
	req.Header.Set("Content-Type", "application/json")

	// Hacer la request
	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("error al enviar la request: %v", err)
	}
	defer resp.Body.Close()

	// Leer la respuesta
	respBody, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("error al leer la respuesta: %v", err)
	}

	fmt.Println("Respuesta CPU interrupción", string(respBody))
	intProcesada.Lock()
	fmt.Println("LOCKJEO")
	// Devolver la respuesta como string
	return string(respBody), nil
}
func executionProces(w http.ResponseWriter, r *http.Request) {

	// Leemos el cuerpo de la solicitud
	body, err := ioutil.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "Error al leer el cuerpo de la solicitud", http.StatusInternalServerError)
		return
	}
	defer r.Body.Close() // Aseguramos cerrar el body

	// Parseamos el JSON del cuerpo en la estructura

	type tcbExecution struct {
		TID     int    `json:"TID"`
		Message string `json:"Message"`
	}
	var tcbExecute tcbExecution
	if err := json.Unmarshal(body, &tcbExecute); err != nil {
		http.Error(w, "Error al parsear el JSON", http.StatusBadRequest)
		return
	}
	fmt.Println("execution", tcbExecute.Message)
	if tcbExecute.Message == "Segmentation fault" {
		err := finalizarProceso(cola_exec[0].PID, true)
		if err == "Error al contactar el Módulo Memoria" {
			http.Error(w, "Error al contactar el Módulo Memoria", http.StatusInternalServerError)
		}
	}
	intProcesada.Unlock()
	Logger.Info(fmt.Sprintf("## (%d:%d) - Solicitó syscall: %s", cola_exec[0].PID, tcbExecute.TID, tcbExecute.Message))
	w.Write([]byte("Info de proceso recibida"))
	w.WriteHeader(http.StatusOK)
}

func planificacionCombinada() {
	var wg sync.WaitGroup
	wg.Add(1)
	// Goroutine que se encarga de planificar y agregar elementos a cola_exec
	go func() {
		defer wg.Done()

		// Escuchar el canal nuevoElementoReady para agregar elementos a la cola_exec
		go func() {

			for {
				select {
				case <-processEnded:
					// Actualizar cola_exec cuando un nuevo elemento esté listo
					fmt.Println("Proceso ended. Actualizando cola exec...")
					fmt.Println("Cola ready inicial", cola_ready)
					fmt.Println("Cola ready multinivel inicial", colasMultinivel)
					var tcbToExec TCB
					cola_exec_mutex.Lock()
					cola_exec = []TCB{}
					switch kernel_config.Scheduler_Algorithm {
					case "FIFO":
						tcbToExec = planificarFifo()
					case "PRIORIDADES":
						tcbToExec = planificarPrioridades()
					case "CMN":
						tcbToExec, _ = planificarColas()
					}

					fmt.Println("HILO NEXT", tcbToExec)
					if planificadorAct && tcbToExec.RutaArchivo != "" {
						cola_exec = append(cola_exec, tcbToExec)
						executeTCB(cola_exec[0])
					} else {
						execEmpty <- true
					}

					if kernel_config.Scheduler_Algorithm == "Colas" && planificadorAct {
						go manejarQuantum(tcbToExec.TID, tcbToExec.PID)
					}
					fmt.Println("Cola exec actualizada:", cola_exec)
					fmt.Println("Cola ready:", cola_ready)
					fmt.Println("Cola mutlnivel fin:", colasMultinivel)
					cola_exec_mutex.Unlock()
				case <-nuevoElementoReadyDesalojo:
					// Actualizar cola_exec cuando un nuevo elemento esté listo
					fmt.Println("Proceso finalizado. Actualizando cola exec...")
					fmt.Println("Cola ready inicial desalojo", cola_ready)
					fmt.Println("Cola ready multinivel inicial  desalojo", colasMultinivel)
					var tcbToExec TCB

					switch kernel_config.Scheduler_Algorithm {
					case "PRIORIDADES":
						tcbToExec = planificarPrioridades()
					case "CMN":
						tcbToExec, _ = planificarColas()
					}
					cola_exec_mutex.Lock()

					if len(cola_exec) > 0 {
						tcbExecutingOld := cola_exec[0]
						processThreadDifferent := tcbToExec.TID != cola_exec[0].TID || tcbToExec.PID != cola_exec[0].PID
						fmt.Println("Process distintos", processThreadDifferent)
						fmt.Println("Planifc", planificadorAct)
						if processThreadDifferent && planificadorAct {
							fmt.Println("Entro al if")
							cola_ready_mutex.Lock()
							cola_ready = append(cola_ready, cola_exec[0]) //Vuelvo a ready el proceso bloqueado
							cola_ready_mutex.Unlock()
							if kernel_config.Scheduler_Algorithm == "Colas" {
								go manejarQuantum(tcbToExec.TID, tcbToExec.PID)
							}
							agregarTCB(cola_exec[0].Prioridad, cola_exec[0])
							notifCPUInterruption(tcbExecutingOld)
							cola_exec[0] = tcbToExec

							executeTCB(cola_exec[0])
						}
					}
					fmt.Println("Cola exec actualizada:", cola_exec)
					fmt.Println("Cola ready fin desalojo:", cola_ready)
					fmt.Println("Cola mutlnivel fin desalojo:", colasMultinivel)
					cola_exec_mutex.Unlock()
				case <-processInterrupted:
					// Actualizar cola_exec cuando un nuevo elemento esté listo
					fmt.Println("Nuevo elemento agregado a processInterrupted. Actualizando cola exec...")
					fmt.Println("Cola ready inicial", cola_ready)
					fmt.Println("Cola ready multinivel inicial", colasMultinivel)
					var tcbToExec TCB
					cola_exec_mutex.Lock()
					var tcbExecuting TCB
					//	fmt.Println("Entro al if")
					if len(cola_exec) > 0 {
						tcbExecuting = cola_exec[0]
						cola_exec = []TCB{}
						cola_ready_mutex.Lock()
						cola_ready = append(cola_ready, tcbExecuting) //Vuelvo a ready el proceso bloqueado
						cola_ready_mutex.Unlock()

						agregarTCB(tcbExecuting.Prioridad, tcbExecuting)
					}
					fmt.Println("Cola ready multinivel dsp de vaciar EXEC INT", colasMultinivel)
					switch kernel_config.Scheduler_Algorithm {
					case "FIFO":
						tcbToExec = planificarFifo()
					case "PRIORIDADES":
						tcbToExec = planificarPrioridades()
					case "CMN":
						tcbToExec, _ = planificarColas()
					}
					fmt.Println("pLANIF", planificadorAct)
					if planificadorAct {
						// Llamada síncrona a notifCPUInterruption
						_, err := notifCPUInterruption(tcbExecuting)
						if err != nil {
							fmt.Println("Error al enviar interrupción:", err)
							return
						}
						// Validar la respuesta y continuar
						intProcesada.Lock()
						fmt.Println("ENTRO AL IF")
						// Agrega el TCB a la cola de ejecución
						cola_exec = append(cola_exec, tcbToExec)

						executeTCB(cola_exec[0])

						// Si usa el algoritmo de colas, manejar el quantum
						if kernel_config.Scheduler_Algorithm == "Colas" {
							go manejarQuantum(tcbToExec.TID, tcbToExec.PID)
						}

						intProcesada.Unlock()
					} else {
						execEmpty <- true
					}

					fmt.Println("Cola exec actualizada interrumpida:", cola_exec)
					fmt.Println("Cola ready fin interrumpida:", cola_ready)
					fmt.Println("Cola mutlnivel fin interrumpida:", colasMultinivel)
					cola_exec_mutex.Unlock()
				default:
					// Esperar sin hacer nada si no hay nuevo
					time.Sleep(time.Millisecond * 500)
				}
			}
		}()

	}()
	wg.Wait() // Esperar a que las gorutinas internas terminen (si aplica)
}
