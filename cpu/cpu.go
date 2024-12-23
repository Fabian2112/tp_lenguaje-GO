package main

import (
	"bytes"
	"cpu/utils"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"sync"
)

// Variables Globales
const interrupcion = false

var Logger *slog.Logger
var CPUCONFIG *utils.CpuConfig
var cpu_libre = true
var esperando_bloqueo = false
var interrupcionMutex sync.Mutex
var cpu_config utils.CpuConfig
var interrupcionChan chan bool
var interruptionExits bool
var Clave utils.Key = utils.Key{PID: -1, TID: -1}
var claveMutex sync.Mutex
var executionMutex sync.Mutex // Mutex para controlar el acceso al ciclo de ejecución
var isExecuting bool = false
var currentClave utils.Key
var currentMutex sync.Mutex

func main() {
	interruptionExits = false
	// Inicializa la configuración del CPU desde un archivo JSON.
	cpu_config = *utils.IniciarConfiguracionCPU("utils/config.json")
	fmt.Println("COnfig", cpu_config.IP_Memory)
	// Configura los endpoints del servidor HTTP.
	mux := http.NewServeMux()
	mux.HandleFunc("PUT /execute", RecibirProceso)
	mux.HandleFunc("POST /interrupcion", interrupccion)

	log.Printf("El CPU server está a la escucha en el puerto %s", fmt.Sprintf("%d", cpu_config.Port))

	// Inicia el servidor HTTP.
	err := http.ListenAndServe(fmt.Sprint(":", cpu_config.Port), mux)
	if err != nil {
		log.Printf("Error al iniciar el servidor HTTP: %v", err)
		panic(err)
	}

}

func RecibirProceso(w http.ResponseWriter, r *http.Request) { //Recibe el tid de ejecucion
	claveMutex.Lock()
	defer claveMutex.Unlock()
	defer r.Body.Close()

	flag := false

	if Clave == (utils.Key{PID: -1, TID: -1}) {
		flag = true
	}

	/*if !cpu_libre {
		http.Error(w, "Error el cpu ya esta en uso", http.StatusInternalServerError)
		return
	}
	cpu_libre = false*/

	// Leer el cuerpo de la solicitud
	var request utils.Key
	err := json.NewDecoder(r.Body).Decode(&request)
	if err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	Clave = utils.Key{
		PID: request.PID,
		TID: request.TID,
	}

	currentClave = Clave

	// Crear el cuerpo de la solicitud para enviar a memoria
	body, err := json.Marshal(request)
	if err != nil {
		http.Error(w, "Error al crear el cuerpo de la solicitud", http.StatusInternalServerError)
		return
	}

	// Enviar el cuerpo de la solicitud a memoria
	url := fmt.Sprintf("http://%s:%d/obtenerContextoEjecucion", cpu_config.IP_Memory, cpu_config.Port_Memory)
	req, err := http.NewRequest("PUT", url, bytes.NewBuffer(body))
	if err != nil {
		http.Error(w, "Error al crear la solicitud a memoria", http.StatusInternalServerError)
		return
	}

	Logger.Info("## TID: %s - Solicito Contexto Ejecución", Clave.TID)
	Logger.Info("## TID: <TID> - Acción: <LEER/ESCRIBIR> - Dirección Física: <DIRECCION_FISICA>")

	cliente := &http.Client{}
	resp, err := cliente.Do(req)
	if err != nil {
		http.Error(w, "Error al n enviar la solicitud a memoria", http.StatusInternalServerError)
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		http.Error(w, "Error en la respuesta de memoria", resp.StatusCode)
		return
	}

	var contexto utils.CPU
	err = json.NewDecoder(resp.Body).Decode(&contexto)
	if err != nil {
		http.Error(w, "Error al decodificar la respuesta de memoria", http.StatusInternalServerError)
		return
	}

	utils.Procesos[Clave] = contexto
	w.WriteHeader(http.StatusOK)
	//interrupcion = false

	if flag {
		go CicloDeEjecucion()
	}

}

func CicloDeEjecucion() {
	var instruccion string

	for !interruptionExits {
		// Fetch: Obtiene la siguiente instrucción
		claveMutex.Lock()
		// Verifica si la clave actual ha cambiado y si no es la misma clave que se estaba ejecutando
		if currentClave != Clave {
			// Si la clave cambió, termina la ejecución actual y sal de la función
			fmt.Println("Clave", currentClave)
			claveMutex.Unlock()
			break
		}
		if Fetch(&instruccion) != 0 {
			fmt.Println("No hay más instruccción")
			break
		}
		claveMutex.Unlock()
		fmt.Printf("\nInstrucción: %s\n", instruccion)

		// Decode: Decodifica la instrucción
		instruccion_verbo, args, syscall := Decode(instruccion)
		if instruccion_verbo == "" {
			fmt.Println("Error al decodificar la instrucción")
			break
		} else {
			if instruccion_verbo != "JNZ" {
				if Execute(instruccion_verbo, args, syscall) == 2 {
					SumarPC(Clave)
				}

			} else {
				SumarPC(Clave)
			}
			SumarPC(Clave)

		}
		ImprimirRegistros()
		CheckInterrupt(Clave)

	}
}

func ImprimirRegistros() {
	proceso, existe := utils.Procesos[Clave]
	if !existe {
		log.Printf("Proceso no encontrado para Clave: %v", Clave)
		return
	}

	registros := proceso.RegistroHilos
	fmt.Printf("Registros del proceso TID: %d\n", Clave.TID)
	fmt.Printf("PC: %d\n", registros.PC)
	fmt.Printf("AX: %d\n", registros.AX)
	fmt.Printf("BX: %d\n", registros.BX)
	fmt.Printf("CX: %d\n", registros.CX)
	fmt.Printf("DX: %d\n", registros.DX)
	fmt.Printf("EX: %d\n", registros.EX)
	fmt.Printf("FX: %d\n", registros.FX)
	fmt.Printf("GX: %d\n", registros.GX)
	fmt.Printf("HX: %d\n", registros.HX)
}

func SumarPC(Clave utils.Key) {
	proceso, existe := utils.Procesos[Clave]
	if !existe {
		log.Printf("Proceso no encontrado para Clave: %v", Clave)
		return
	}

	proceso.RegistroHilos.PC += 1
	utils.Procesos[Clave] = proceso
	log.Printf("## TID: %s - PC incrementado a: %d", Clave.TID, proceso.RegistroHilos.PC)
}
func Fetch(instruccion *string) int {
	cliente := &http.Client{}

	if instruccion == nil {
		log.Println("Error: puntero instruccion es nil")
		return -1
	}

	// Construir la URL de la solicitud a la memoria
	url := fmt.Sprintf("http://%s:%d/obtenerInstruccion", cpu_config.IP_Memory, cpu_config.Port_Memory)

	// Crear la estructura para el cuerpo de la solicitud con PID, TID y PC

	requestBody := map[string]int{
		"pid": Clave.PID,
		"tid": Clave.TID,
		"pc":  utils.Procesos[Clave].RegistroHilos.PC,
	}

	Logger.Info("## TID: %s - FETCH - Program Counter: %d", Clave.TID, utils.Procesos[Clave].RegistroHilos.PC)

	// Convertir la estructura en un cuerpo JSON
	body, err := json.Marshal(requestBody)
	if err != nil {
		log.Println("Error al crear el cuerpo de la solicitud:", err)
		return -1
	}

	// Crear la solicitud POST
	req, err := http.NewRequest("POST", url, bytes.NewBuffer(body))
	if err != nil {
		log.Println("Error creando solicitud:", err)
		return -1
	}

	// Establecer el encabezado Content-Type
	req.Header.Set("Content-Type", "application/json")

	// Enviar la solicitud a la memoria
	respuesta, err := cliente.Do(req)
	if err != nil {
		log.Println("Error al enviar solicitud:", err)
		return -1
	}
	defer respuesta.Body.Close()

	// Verificar el código de estado de la respuesta
	if respuesta.StatusCode != http.StatusOK {
		log.Println("No hay más instrucciones")
		return -1
	}

	// Leer el cuerpo de la respuesta
	bodyBytes, err := io.ReadAll(respuesta.Body)
	if err != nil {
		log.Println("Error al leer cuerpo de la respuesta:", err)
		return -1
	}

	// Verificar si la respuesta está vacía
	if len(bodyBytes) == 0 {
		log.Println("Error: respuesta del servidor vacía")
		return -1
	}

	// Definir una estructura para la respuesta de la memoria
	var response map[string]string
	err = json.Unmarshal(bodyBytes, &response)
	if err != nil {
		log.Println("Error al decodificar la respuesta JSON:", err)
		return -1
	}

	// Extraer la instrucción de la respuesta
	instruccionRecibida, ok := response["instruccion"]
	if !ok {
		log.Println("Error: la instrucción no está en la respuesta")
		return -1
	}

	log.Printf("## Instruccion: %s", instruccionRecibida)

	// Asignar la instrucción al puntero recibido
	*instruccion = instruccionRecibida

	// Devolver 0 para indicar que la instrucción fue recibida correctamente
	return 0
}

func Decode(instruccion string) (string, []string, int) {
	// Divide la instrucción en partes: el verbo y sus argumentos
	words := strings.Fields(instruccion)
	if len(words) < 1 {
		log.Println("Error: instrucción incompleta")
		return "", nil, -1
	}

	// El primer elemento es el verbo de la instrucción
	instruccion_decode := strings.TrimSpace(words[0])

	// Resto de elementos son los argumentos
	args := words[1:]
	fmt.Println("INS", instruccion_decode)
	fmt.Println("ARGS", args)
	// Validamos si la instrucción es un Syscall
	switch instruccion_decode {
	case "DUMP_MEMORY", "IO", "PROCESS_CREATE", "THREAD_CREATE",
		"THREAD_JOIN", "THREAD_CANCEL", "MUTEX_CREATE", "MUTEX_LOCK",
		"MUTEX_UNLOCK", "THREAD_EXIT", "PROCESS_EXIT":
		return instruccion_decode, args, 1 // Indica que es un Syscall

	case "WRITE_MEM":
		dire := utils.ObtenerValorRegistro(Clave, args[0])

		direccionFisica, err := TraducirDireccion(dire, Clave)
		if err != nil {
			log.Printf("Error al traducir dirección lógica a física: %v", err)
			return "", nil, -1
		}

		log.Println("direccion fisica:", direccionFisica)
		args[0] = strconv.Itoa(direccionFisica)

		return instruccion_decode, args, 1

	case "READ_MEM":
		dire := utils.ObtenerValorRegistro(Clave, args[1])

		direccionFisica, err := TraducirDireccion(dire, Clave)
		if err != nil {
			log.Printf("Error al traducir dirección lógica a física: %v", err)
			return "", nil, -1
		}

		log.Println("direccion fisica:", direccionFisica)
		args[1] = strconv.Itoa(direccionFisica)

		return instruccion_decode, args, 1

	default:
		return instruccion_decode, args, 0 // Indica que es una instrucción normal
	}
}

func Execute(verbo string, args []string, syscall int) int {

	Logger.Info("## TID: %s - Ejecutando: %s - %v", Clave.TID, verbo, args)

	switch verbo {
	case "SET":
		if len(args) != 2 {
			log.Println("Error: SET requiere 2 argumentos")
			return -1
		}
		return utils.SET(args, Clave)

	case "SUM":
		if len(args) != 2 {
			log.Println("Error: SUM requiere 2 argumentos")
			return -1
		}
		return utils.SUM(args, Clave)

	case "SUB":
		if len(args) != 2 {
			log.Println("Error: SUB requiere 2 argumentos")
			return -1
		}
		return utils.SUB(args, Clave)

	case "READ_MEM":
		if len(args) != 2 {
			log.Println("Error: READ_MEM requiere 2 argumentos")
			return -1
		}
		return utils.READ_MEM(args, Clave)

	case "WRITE_MEM":
		if len(args) != 2 {
			log.Println("Error: WRITE_MEM requiere 2 argumentos")
			return -1
		}
		return utils.WRITE_MEM(args, Clave)

	case "JNZ":
		if len(args) != 2 {
			log.Println("Error: JNZ requiere 2 argumentos")
			return -1
		}
		return utils.JNZ(args, Clave)

	case "LOG":
		if len(args) != 1 {
			log.Println("Error: LOG requiere 1 argumento")
			return -1
		}
		return utils.LOG(args, Clave)

	case "DUMP_MEMORY":
		_, err := utils.DUMP_MEMORY()
		if err != nil {
			log.Println("Error: DUMP_MEMORY falló", err)
			return -1
		}
		ActualizarContextoEnMemoria()
		return 0

	case "IO":
		if len(args) != 1 {
			log.Println("Error: IO requiere 1 argumento")
			return -1
		}

		// Convertir el argumento de string a int
		tiempo, err := strconv.Atoi(args[0])
		if err != nil {
			log.Printf("Error: argumento 'tiempo' inválido '%s'\n", args[0])
			return -1
		}

		// Llamar a la función IO pasando el argumento tiempo
		_, err = utils.IO(tiempo)
		if err != nil {
			log.Printf("Error en IO: %v", err)
			return -1
		}
		ActualizarContextoEnMemoria()
		return 0

	case "PROCESS_CREATE":
		if len(args) != 3 {
			log.Println("Error: PROCESS_CREATE requiere 3 argumentos")
			return -1
		}
		_, err := utils.PROCESS_CREATE(args)
		if err != nil {
			log.Println("Error: PROCESS_CREATE falló", err)
			return -1
		}
		ActualizarContextoEnMemoria()
		return 0

	case "THREAD_CREATE":
		if len(args) != 2 {
			log.Println("Error: THREAD_CREATE requiere 2 argumentos")
			return -1
		}
		resp, err := utils.THREAD_CREATE(args)
		fmt.Println("RESP CREATE", resp)
		if err != nil {
			log.Println("Error: THREAD_CREATE falló", err)
			return -1
		}
		ActualizarContextoEnMemoria()
		return 0

	case "THREAD_JOIN":
		if len(args) != 1 {
			log.Println("Error: THREAD_JOIN requiere 1 argumento")
			return -1
		}
		_, err := utils.THREAD_JOIN(args)
		if err != nil {
			log.Println("Error: THREAD_JOIN falló", err)
			return -1
		}
		ActualizarContextoEnMemoria()
		return 0

	case "THREAD_CANCEL":
		if len(args) != 1 {
			log.Println("Error: THREAD_CANCEL requiere 1 argumento")
			return -1
		}
		_, err := utils.THREAD_CANCEL(args)
		if err != nil {
			log.Println("Error: THREAD_CANCEL falló", err)
			return -1
		}
		ActualizarContextoEnMemoria()
		return 0

	case "MUTEX_CREATE":
		if len(args) != 1 {
			log.Println("Error: MUTEX_CREATE requiere 1 argumento")
			return -1
		}
		err := utils.MUTEX_CREATE(args)
		if err != "" {
			log.Println("Error: MUTEX_CREATE falló", err)
			return -1
		}
		ActualizarContextoEnMemoria()
		return 0

	case "MUTEX_LOCK":
		if len(args) != 1 {
			log.Println("Error: MUTEX_LOCK requiere 1 argumento")
			return -1
		}
		_, err := utils.MUTEX_LOCK(args)
		if err != nil {
			log.Println("Error: MUTEX_LOCK falló", err)
			return -1
		}
		ActualizarContextoEnMemoria()
		return 0

	case "MUTEX_UNLOCK":
		if len(args) != 1 {
			log.Println("Error: MUTEX_UNLOCK requiere 1 argumento")
			return -1
		}
		_, err := utils.MUTEX_UNLOCK(args)
		if err != nil {
			log.Println("Error: MUTEX_UNLOCK falló", err)
			return -1
		}
		ActualizarContextoEnMemoria()
		return 0

	case "THREAD_EXIT":
		_, err := utils.THREAD_EXIT(args)
		if err != nil {
			log.Println("Error: THREAD_EXIT falló", err)
			return -1
		}

		ActualizarContextoEnMemoria()
		return 0

	case "PROCESS_EXIT":
		_, err := utils.PROCESS_EXIT(args)
		if err != nil {
			log.Println("Error: PROCESS_EXIT falló", err)
			return -1
		}

		ActualizarContextoEnMemoria()
		return 0
	default:
		log.Printf("Error: instrucción desconocida '%s'\n", verbo)
		return -1
	}
}

func TraducirDireccion(direccionLogica int, Clave utils.Key) (int, error) {
	// Obtener el proceso correspondiente a la Clave
	proceso, existe := utils.Procesos[Clave]
	if !existe {
		return -1, fmt.Errorf("proceso no encontrado")
	}

	// Validar que la dirección lógica esté dentro del límite
	if direccionLogica < 0 || direccionLogica >= proceso.Limit {
		fmt.Println("Segmentation Fault: Dirección lógica fuera de límites")
		ManejarSegmentationFault(Clave)
		return -1, fmt.Errorf("segmentation fault")
	}

	// Traducir dirección lógica a física
	direccionFisica := proceso.Base + direccionLogica
	fmt.Printf("Dirección lógica %d traducida a física %d\n", direccionLogica, direccionFisica)

	return direccionFisica, nil
}

func ManejarSegmentationFault(Clave utils.Key) {
	// Guardar el contexto del hilo en memoria
	fmt.Println("Guardando contexto de ejecución debido a Segmentation Fault")
	err := ActualizarContextoEnMemoria()
	if err != nil {
		fmt.Println("Error al actualizar el contexto en memoria:", err)
	}
	Logger.Info("## TID: <TID> - Acción: <LEER / ESCRIBIR> - Dirección Física: <DIRECCION_FISICA>")
	// Notificar al kernel sobre el Segmentation Fault
	NotificarKernelInterrupcion(Clave.TID, "Segmentation fault")

	// Marcar la CPU como libre
	cpu_libre = true
}

func NotificarKernelSegmentationFault(tid int) {
	fmt.Println("Notificando al kernel sobre Segmentation Fault para TID:", tid)

	cliente := &http.Client{}
	url := fmt.Sprintf("http://%s:%d/segfault", cpu_config.IP_Kernel, cpu_config.Port_Kernel)

	// Crear cuerpo de la solicitud
	body, _ := json.Marshal(map[string]int{"tid": tid})
	req, err := http.NewRequest("POST", url, bytes.NewBuffer(body))
	if err != nil {
		fmt.Println("Error al crear la solicitud de Segmentation Fault:", err)
		return
	}

	req.Header.Set("Content-Type", "application/json")
	resp, err := cliente.Do(req)
	if err != nil {
		fmt.Println("Error al enviar la notificación de Segmentation Fault al kernel:", err)
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusOK {
		fmt.Println("Kernel notificado correctamente sobre Segmentation Fault para TID:", tid)
	} else {
		fmt.Println("Error al notificar al kernel:", resp.StatusCode)
	}
}

func ActualizarContextoEnMemoria() error {
	// Obtener el contexto actual del proceso
	contexto, existe := utils.Procesos[Clave]
	if !existe {
		return fmt.Errorf("proceso no encontrado")
	}

	// Crear el cuerpo de la solicitud con el contexto actualizado
	body, err := json.Marshal(contexto)
	if err != nil {
		return fmt.Errorf("error al crear el cuerpo de la solicitud: %v", err)
	}

	// Construir la URL de la solicitud a la memoria
	url := fmt.Sprintf("http://%s:%d/actualizarContextoEjecucion", cpu_config.IP_Memory, cpu_config.Port_Memory)

	// Crear la solicitud PUT
	req, err := http.NewRequest("PUT", url, bytes.NewBuffer(body))
	if err != nil {
		return fmt.Errorf("error al crear la solicitud: %v", err)
	}

	// Establecer el encabezado Content-Type
	req.Header.Set("Content-Type", "application/json")

	// Enviar la solicitud a la memoria
	cliente := &http.Client{}
	resp, err := cliente.Do(req)
	if err != nil {
		return fmt.Errorf("error al enviar la solicitud: %v", err)
	}
	defer resp.Body.Close()

	// Verificar el código de estado de la respuesta
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("error en la respuesta de memoria: %v", resp.StatusCode)
	}

	Logger.Info("## TID: %s - Actualizo Contexto Ejecución", Clave.TID)
	Logger.Info("## TID: <TID> - Acción: <LEER / ESCRIBIR> - Dirección Física: <DIRECCION_FISICA>")
	return nil
}

func interrupccion(w http.ResponseWriter, r *http.Request) {
	// Primero obtengo la info del proceso a crear
	type ProcessInterrupcion struct {
		PID     int    `json:"PID"`
		TID     int    `json:"TID"`
		Mensaje string `json:"Mensaje"`
	}
	var bodyInterrupcion ProcessInterrupcion
	err := json.NewDecoder(r.Body).Decode(&bodyInterrupcion)
	if err != nil {
		http.Error(w, "Error al decodificar JSON", http.StatusBadRequest)
		return
	}
	interrupcionMutex.Lock()
	interruptionExits = true
	interrupcionMutex.Unlock()
	// Notificar al canal que se recibió la interrupción
	Logger.Info("## Llega interrupcion al puerto Interrupt")
	// Esperar la señal desde el canal

	log.Printf("Int ya procesada")
	w.WriteHeader(http.StatusOK)
	w.Write([]byte("Interrupción procesada"))

}

func CheckInterrupt(Clave utils.Key) {
	interrupcionMutex.Lock()
	defer interrupcionMutex.Unlock()
	if interruptionExits {
		Logger.Info("## Llega interrupcion al puerto Interrupt")
		err := ActualizarContextoEnMemoria()
		if err != nil {
			log.Println("Error al actualizar el contexto en memoria:", err)
			return
		}

		interruptionExits = false

		NotificarKernelInterrupcion(Clave.TID, "Interrupción recibida")
		// Notificar a la función que ha terminado el procesamiento de la interrupción

	} else {
		log.Printf("## TID: %s - No hay interrupción", Clave.TID)
	}

}

func NotificarKernelInterrupcion(tid int, mensajeContextoProceso string) {
	cliente := &http.Client{}
	url := fmt.Sprintf("http://%s:%d/executionProcess", cpu_config.IP_Kernel, cpu_config.Port_Kernel)
	type tcbExecution struct {
		TID     int    `json:"TID"`
		Message string `json:"Message"`
	}
	bodyExecution := tcbExecution{
		TID:     tid,
		Message: mensajeContextoProceso,
	}
	// Crear cuerpo de la solicitud
	body, _ := json.Marshal(bodyExecution)
	req, err := http.NewRequest("POST", url, bytes.NewBuffer(body))
	if err != nil {
		log.Println("Error al crear la solicitud de interrupción:", err)
		return
	}

	req.Header.Set("Content-Type", "application/json")
	resp, err := cliente.Do(req)
	if err != nil {
		log.Println("Error al enviar la notificación de interrupción al kernel:", err)
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusOK {
		log.Println("Kernel notificado correctamente sobre la interrupción para TID:", tid)
	} else {
		log.Println("Error al notificar al kernel:", resp.StatusCode)
	}
}
