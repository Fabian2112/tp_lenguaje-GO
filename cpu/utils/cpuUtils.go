package utils

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"io/ioutil"
	"log"
	"net/http"
	"net/url"
	"os"
	"reflect"
	"strconv"
)

type CPU struct {
	Base          int // Dirección base
	Limit         int // Límite de la dirección
	RegistroHilos RegistroHilos
}

type RegistroHilos struct {
	AX int // Registros
	BX int
	CX int
	DX int
	EX int
	FX int
	GX int
	HX int
	PC int // Program Counter
}

type Key struct {
	PID int
	TID int
}

var CpuConfigGlobal *CpuConfig

type CpuConfig struct {
	IP_Memory   string `json:"ip_memory"`
	Port_Memory int    `json:"port_memory"`
	IP_Kernel   string `json:"ip_kernel"`
	Port_Kernel int    `json:"port_kernel"`
	Port        int    `json:"port"`
	Log_Level   string `json:"log_level"`
}

var cpu_config CpuConfig

type Registros struct {
	registrosHilos *RegistroHilos
	Direccion      int
	Datos          byte
}

const LOG_FILE = "log.txt"

var Procesos = make(map[Key]CPU)

func main() {
	cpu_config = *IniciarConfiguracionCPU("utils/config.json")
	fmt.Println(cpu_config)
}
func IniciarConfiguracionCPU(filePath string) *CpuConfig {
	var config *CpuConfig
	CpuConfigGlobal = &CpuConfig{} // Inicializamos el puntero a la estructura
	configFile, err := os.Open(filePath)
	if err != nil {
		log.Fatal(err.Error())
	}
	defer configFile.Close()

	jsonParser := json.NewDecoder(configFile)
	jsonParser.Decode(&config)
	CpuConfigGlobal = config
	fmt.Println("IOP", CpuConfigGlobal.IP_Kernel)
	return config
}

// Función para obtener un registro
func obtenerRegistro(clave Key, reg_name string) (reflect.Value, error) {
	fmt.Printf("Buscando registro: %s\n", reg_name)

	// Obtener los registros del mapa de procesos
	registros, existe := Procesos[clave]
	if !existe {
		return reflect.Value{}, fmt.Errorf("proceso no encontrado para la clave: %v", clave)
	}

	// Obtener el campo por nombre
	reg := reflect.ValueOf(&registros.RegistroHilos).Elem().FieldByName(reg_name)

	// Verificar si el campo es válido
	if !reg.IsValid() {
		// Depuración en caso de que el campo no sea válido
		fmt.Printf("Error: registro '%s' no encontrado\n", reg_name)
		return reflect.Value{}, fmt.Errorf("registro no encontrado: %s", reg_name)
	}

	// Verificar que el campo sea de tipo int
	if reg.Kind() != reflect.Int {
		// Depuración en caso de que el tipo no sea int
		fmt.Printf("Error: registro '%s' no es del tipo esperado (int)\n", reg_name)
		return reflect.Value{}, fmt.Errorf("registro '%s' no es del tipo esperado (int)", reg_name)
	}

	// Verificar si el campo puede ser modificado
	if !reg.CanSet() {
		// Depuración en caso de que el campo no sea modificable
		fmt.Printf("Error: registro '%s' no es modificable\n", reg_name)
		return reflect.Value{}, fmt.Errorf("registro '%s' no es modificable", reg_name)
	}

	// Depuración de éxito al encontrar y validar el registro
	fmt.Printf("Registro '%s' encontrado y es válido\n", reg_name)

	return reg, nil
}

// Función para modificar un registro
func modificarRegistro(clave Key, reg_name string, valor int) error {

	// Obtener el registro usando la función reflect
	reg, err := obtenerRegistro(clave, reg_name)
	if err != nil {
		return err
	}

	// Verificar que el campo obtenido sea de tipo int
	if reg.Kind() != reflect.Int {
		return fmt.Errorf("el registro '%s' no es de tipo int", reg_name)
	}

	reg.SetInt(int64(valor))
	return nil
}

// Implementación de instrucciones normales

func SET(args []string, clave Key) int {
	registros := Procesos[clave].RegistroHilos
	ImprimirRegistros(registros, clave.PID)

	registro := args[0]
	valor, err := strconv.Atoi(args[1])
	if err != nil {
		log.Printf("Error: SET requiere un valor numérico válido, obtenido '%s'\n", args[1])
		return -1
	}
	AsignarValorRegistro(clave, registro, valor)
	fmt.Printf("SET: Registro %s asignado a %d\n", registro, valor)
	return 1
}

func READ_MEM(args []string, clave Key) int {
	regDatos := args[0]
	direccion, err := strconv.Atoi(args[1])
	if err != nil {
		log.Printf("Error: READ_MEM requiere una dirección numérica válida, obtenida '%s'\n", args[1])
		return -1
	}

	valor, err := LeerDeMemoria(direccion)
	if err != nil {
		log.Printf("Error: no se pudo leer de la dirección %d en memoria\n", direccion)
		return -1
	}

	AsignarValorRegistro(clave, regDatos, valor)
	fmt.Printf("READ_MEM: Registro %s actualizado con el valor %d de la dirección %d\n", regDatos, valor, direccion)
	return 1
}

func WRITE_MEM(args []string, clave Key) int {
	direccion, err := strconv.Atoi(args[0])
	if err != nil {
		log.Printf("Error: READ_MEM requiere una dirección numérica válida, obtenida '%s'\n", args[1])
		return -1
	}
	regDatos := args[1]

	log.Printf("WRITE_MEM: regDireccion = %d, regDatos = %s\n", direccion, regDatos)
	valor := ObtenerValorRegistro(clave, regDatos)

	err = EscribirEnMemoria(direccion, valor)
	if err != nil {
		log.Printf("Error: no se pudo escribir el valor %d en la dirección %d\n", valor, direccion)
		return -1
	}

	fmt.Printf("WRITE_MEM: Escrito %d en la dirección %d\n", valor, direccion)
	return 1
}

func SUM(args []string, clave Key) int {
	regDestino := args[0]
	regOrigen := args[1]

	valorDestino := ObtenerValorRegistro(clave, regDestino)
	valorOrigen := ObtenerValorRegistro(clave, regOrigen)

	AsignarValorRegistro(clave, regDestino, valorDestino+valorOrigen)
	fmt.Printf("SUM: Registro %s actualizado a %d\n", regDestino, valorDestino+valorOrigen)
	return 1
}

func SUB(args []string, clave Key) int {
	regDestino := args[0]
	regOrigen := args[1]

	valorDestino := ObtenerValorRegistro(clave, regDestino)
	valorOrigen := ObtenerValorRegistro(clave, regOrigen)

	AsignarValorRegistro(clave, regDestino, valorDestino-valorOrigen)
	fmt.Printf("SUB: Registro %s actualizado a %d\n", regDestino, valorDestino-valorOrigen)
	return 1
}

func JNZ(args []string, clave Key) int {
	/*registro := args[0]
	instruccion, err := strconv.Atoi(args[1])
	if err != nil {
		log.Printf("Error: JNZ requiere un número de instrucción válido, obtenido '%s'\n", args[1])
		return -1
	}

	if ObtenerValorRegistro(clave, registro) != 0 {
		AsignarValorRegistro(clave, "PC", instruccion)
		fmt.Printf("JNZ: Program Counter actualizado a %d\n", instruccion)
		return 1
	} else {
		fmt.Println("JNZ: Condición no cumplida, el Program Counter no se actualiza")
		return 2
	}*/
	return 1
}

func LOG(args []string, clave Key) int {
	registro := args[0]
	valor := ObtenerValorRegistro(clave, registro)

	// Abrir el archivo de log en modo append
	file, err := os.OpenFile(LOG_FILE, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		log.Printf("Error al abrir el archivo de log: %v\n", err)
		return -1
	}
	defer file.Close()

	// Escribir en el archivo de log
	logMessage := fmt.Sprintf("LOG: Clave PID=%d, TID=%d, Registro %s = %d\n", clave.PID, clave.TID, registro, valor)
	if _, err := file.WriteString(logMessage); err != nil {
		log.Printf("Error al escribir en el archivo de log: %v\n", err)
		return -1
	}

	fmt.Print(logMessage)
	return 1
}

func LeerDeMemoria(direccion int) (int, error) {
	// Crear la URL con la dirección IP y el puerto
	url := fmt.Sprintf("http://%s:%d/readMem", CpuConfigGlobal.IP_Memory, CpuConfigGlobal.Port_Memory)

	// Crear el cuerpo de la solicitud con la dirección física
	requestBody := map[string]int{
		"direccionFisica": direccion,
	}

	// Serializar el cuerpo de la solicitud a JSON
	jsonData, err := json.Marshal(requestBody)
	if err != nil {
		// Devolver error si la serialización falla
		return 0, fmt.Errorf("error al serializar datos: %v", err)
	}

	// Realizar la solicitud POST
	resp, err := http.Post(url, "application/json", bytes.NewBuffer(jsonData))
	if err != nil {
		// Devolver error si no se puede realizar la solicitud
		return 0, fmt.Errorf("error al realizar la solicitud: %v", err)
	}
	defer resp.Body.Close()

	// Verificar si la respuesta es exitosa
	if resp.StatusCode != http.StatusOK {
		return 0, fmt.Errorf("error en la respuesta del servidor: %v", resp.Status)
	}

	// Leer la respuesta del cuerpo
	body, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		// Devolver error si no se puede leer la respuesta
		return 0, fmt.Errorf("error al leer la respuesta: %v", err)
	}

	// Deserializar la respuesta a un mapa de clave 'valor'
	var result map[string]int
	if err := json.Unmarshal(body, &result); err != nil {
		// Devolver error si la deserialización falla
		return 0, fmt.Errorf("error al deserializar la respuesta: %v", err)
	}

	// Retornar el valor leído de memoria
	return result["valor"], nil
}

func EscribirEnMemoria(direccion int, valor int) error {
	// Crear la URL con la dirección IP y el puerto
	baseURL := fmt.Sprintf("http://%s:%d/writeMem", CpuConfigGlobal.IP_Memory, CpuConfigGlobal.Port_Memory)

	// Estructura del cuerpo de la solicitud
	type RequestBody struct {
		DireccionFisica int    `json:"direccionFisica"` // Dirección inicial donde escribir
		Datos           []byte `json:"datos"`           // Array con los bytes a escribir (esperamos 4 bytes)
	}

	// Crear el cuerpo de la solicitud, convirtiendo el valor entero a 4 bytes
	requestBody := RequestBody{
		DireccionFisica: direccion,
		Datos:           []byte{byte(valor >> 24), byte(valor >> 16), byte(valor >> 8), byte(valor)},
	}

	// Serializar el cuerpo a JSON
	jsonBytes, err := json.Marshal(requestBody)
	if err != nil {
		return fmt.Errorf("error al convertir el struct a JSON: %v", err)
	}

	// Crear la solicitud POST
	req, err := http.NewRequest("POST", baseURL, bytes.NewBuffer(jsonBytes))
	if err != nil {
		return fmt.Errorf("error al crear la request: %v", err)
	}

	// Establecer el Content-Type como application/json
	req.Header.Set("Content-Type", "application/json")

	// Hacer la solicitud
	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("error al enviar la request: %v", err)
	}
	defer resp.Body.Close()

	// Verificar el código de estado de la respuesta
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("error en la respuesta de la memoria: %s", resp.Status)
	}

	return nil
}

func ObtenerValorRegistro(clave Key, registro string) int {
	registros := Procesos[clave].RegistroHilos
	switch registro {
	case "AX":
		return registros.AX
	case "BX":
		return registros.BX
	case "CX":
		return registros.CX
	case "DX":
		return registros.DX
	case "EX":
		return registros.EX
	case "FX":
		return registros.FX
	case "GX":
		return registros.GX
	case "HX":
		return registros.HX
	case "PC":
		return registros.PC
	default:
		log.Printf("Error: registro '%s' no válido\n", registro)
		return 0
	}
}

func AsignarValorRegistro(clave Key, registro string, valor int) {
	registros := Procesos[clave].RegistroHilos
	switch registro {
	case "AX":
		registros.AX = valor
	case "BX":
		registros.BX = valor
	case "CX":
		registros.CX = valor
	case "DX":
		registros.DX = valor
	case "EX":
		registros.EX = valor
	case "FX":
		registros.FX = valor
	case "GX":
		registros.GX = valor
	case "HX":
		registros.HX = valor
	case "PC":
		registros.PC = valor
	default:
		log.Printf("Error: registro '%s' no válido para asignación\n", registro)
	}
	Procesos[clave] = CPU{Base: Procesos[clave].Base, Limit: Procesos[clave].Limit, RegistroHilos: registros}
}

func ImprimirRegistros(registros RegistroHilos, pid int) {
	//IMPRIMIR ESTADO DE REGISTROS
	fmt.Println("\nAX: ", registros.AX, " | ", "EX: ", registros.EX)
	fmt.Println("BX: ", registros.BX, " | ", "FX: ", registros.FX)
	fmt.Println("CX: ", registros.CX, " | ", "GX: ", registros.GX)
	fmt.Println("DX: ", registros.DX, " | ", "HX: ", registros.HX)

	fmt.Println("PC: ", registros.PC, " | ", "PID: ", pid)
	fmt.Println("----------------------------")
}

func IO(tiempo int) (string, error) {
	// Crear la URL con la dirección IP y el puerto
	baseURL := fmt.Sprintf("http://%s:%d/IO", CpuConfigGlobal.IP_Kernel, CpuConfigGlobal.Port_Kernel)

	// Parsear la URL
	u, err := url.Parse(baseURL)
	if err != nil {
		return "", fmt.Errorf("error al parsear la URL: %v", err)
	}

	// Agregar el query parameter
	q := u.Query()
	q.Set("segundos", string(tiempo)) // Reemplaza "param1" y "valor1" según tus necesidades
	u.RawQuery = q.Encode()

	// Crear la request POST
	req, err := http.NewRequest("POST", u.String(), nil)
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

	// Manejo de respuesta (si es necesario)
	return "Request exitosa", nil
}

type threadToCreate struct {
	RutaArchivo string
	SizeProceso int
	Prioridad   int
}

func THREAD_CREATE(args []string) (string, error) {
	if len(args) != 2 {
		return "", fmt.Errorf("THREAD_CREATE requiere 2 argumentos")
	}
	fmt.Println("MANDO CREAR")
	// Extraer los parámetros
	archivo := args[0]
	prioridad, err := strconv.Atoi(args[1])
	if err != nil {
		return "", fmt.Errorf("Error al convertir prioridad '%s' a entero: %v", args[1], err)
	}

	// Crear la URL con la dirección IP y el puerto
	url := fmt.Sprintf("http://%s:%d/thread_create", CpuConfigGlobal.IP_Kernel, CpuConfigGlobal.Port_Kernel)

	// Crear el cuerpo de la solicitud
	cuerpoRequest := threadToCreate{
		RutaArchivo: archivo,
		SizeProceso: 0, // El tamaño del hilo no se pasa como argumento, así que lo ponemos en 0
		Prioridad:   prioridad,
	}

	// Convertir a JSON
	jsonBytes, err := json.Marshal(cuerpoRequest)
	if err != nil {
		return "", fmt.Errorf("error al convertir el struct a JSON: %v", err)
	}

	// Crear la solicitud PUT
	req, err := http.NewRequest("PUT", url, bytes.NewBuffer(jsonBytes))
	if err != nil {
		return "", fmt.Errorf("error al crear la request: %v", err)
	}

	// Establecer Content-Type
	req.Header.Set("Content-Type", "application/json")

	// Enviar la solicitud
	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("error al enviar la request: %v", err)
	}
	defer resp.Body.Close()

	// Manejo de respuesta
	return fmt.Sprintf("Hilo creado con archivo: %s y prioridad: %d", archivo, prioridad), nil
}

type processToCreate struct {
	RutaArchivo string
	SizeProceso int
	Prioridad   int
}

func PROCESS_CREATE(args []string) (string, error) {
	if len(args) != 3 {
		return "", fmt.Errorf("PROCESS_CREATE requiere 3 argumentos")
	}

	// Extraer los parámetros
	archivo := args[0]
	tamanio, err := strconv.Atoi(args[1])
	if err != nil {
		return "", fmt.Errorf("Error al convertir tamaño '%s' a entero: %v", args[1], err)
	}
	prioridad, err := strconv.Atoi(args[2])
	if err != nil {
		return "", fmt.Errorf("Error al convertir prioridad '%s' a entero: %v", args[2], err)
	}

	// Crear la URL con la dirección IP y el puerto
	url := fmt.Sprintf("http://%s:%d/createProcess", CpuConfigGlobal.IP_Kernel, CpuConfigGlobal.Port_Kernel)

	// Crear el cuerpo de la solicitud
	cuerpoRequest := processToCreate{
		RutaArchivo: archivo,
		SizeProceso: tamanio,
		Prioridad:   prioridad,
	}

	// Convertir a JSON
	jsonBytes, err := json.Marshal(cuerpoRequest)
	if err != nil {
		return "", fmt.Errorf("error al convertir el struct a JSON: %v", err)
	}

	// Crear la solicitud PUT
	req, err := http.NewRequest("PUT", url, bytes.NewBuffer(jsonBytes))
	if err != nil {
		return "", fmt.Errorf("error al crear la request: %v", err)
	}

	// Establecer Content-Type
	req.Header.Set("Content-Type", "application/json")

	// Enviar la solicitud
	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("error al enviar la request: %v", err)
	}
	defer resp.Body.Close()

	// Manejo de respuesta
	return fmt.Sprintf("Proceso creado con archivo: %s y prioridad: %d", archivo, prioridad), nil
}

func DUMP_MEMORY() (string, error) {

	// Crear la URL con la dirección IP y el puerto desde los registros o configuración global
	url := fmt.Sprintf("http://%s:%d/dumpMemory", CpuConfigGlobal.IP_Kernel, CpuConfigGlobal.Port_Kernel)

	req, err := http.NewRequest("POST", url, nil)
	if err != nil {
		return "", fmt.Errorf("Error al crear la request: %v", err)
	}

	req.Header.Set("Content-Type", "application/json")

	// Enviar la request
	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("Error al enviar la request: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("Error: respuesta inesperada del servidor, código: %d", resp.StatusCode)
	}

	// Leer la respuesta
	responseBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("Error al leer la respuesta: %v", err)
	}

	return string(responseBody), nil
}

// THREAD_JOIN se encarga de realizar la solicitud HTTP para esperar a que el hilo termine.
func THREAD_JOIN(args []string) (string, error) {
	if len(args) != 1 {
		return "", fmt.Errorf("THREAD_JOIN requiere 1 argumento")
	}

	// Extraer el TID del argumento
	tid, err := strconv.Atoi(args[0])
	if err != nil {
		return "", fmt.Errorf("Error al convertir TID '%s' a entero: %v", args[0], err)
	}

	// Crear la URL con la dirección IP y el puerto
	baseURL := fmt.Sprintf("http://%s:%d/thread_join", CpuConfigGlobal.IP_Kernel, CpuConfigGlobal.Port_Kernel)

	// Parsear la URL
	u, err := url.Parse(baseURL)
	if err != nil {
		return "", fmt.Errorf("error al parsear la URL: %v", err)
	}

	// Agregar el query parameter con el TID
	q := u.Query()
	q.Set("tid", strconv.Itoa(tid)) // Usamos el TID convertido a string
	u.RawQuery = q.Encode()

	// Crear la solicitud HTTP
	req, err := http.NewRequest("POST", u.String(), nil)
	if err != nil {
		return "", fmt.Errorf("error al crear la request: %v", err)
	}

	// Establecer el Content-Type como JSON
	req.Header.Set("Content-Type", "application/json")

	// Enviar la solicitud HTTP
	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("error al enviar la request: %v", err)
	}
	defer resp.Body.Close()

	// Manejo de respuesta (si es necesario)
	return fmt.Sprintf("Request exitosa para el TID: %d", tid), nil
}

// THREAD_CANCEL se encarga de realizar la solicitud HTTP para cancelar el hilo.
func THREAD_CANCEL(args []string) (string, error) {
	if len(args) != 1 {
		return "", fmt.Errorf("THREAD_CANCEL requiere 1 argumento")
	}

	// Extraer el TID del argumento
	tid, err := strconv.Atoi(args[0])
	if err != nil {
		return "", fmt.Errorf("Error al convertir TID '%s' a entero: %v", args[0], err)
	}

	// Crear la URL con la dirección IP y el puerto
	baseURL := fmt.Sprintf("http://%s:%d/thread_cancel", CpuConfigGlobal.IP_Kernel, CpuConfigGlobal.Port_Kernel)

	// Parsear la URL
	u, err := url.Parse(baseURL)
	if err != nil {
		return "", fmt.Errorf("error al parsear la URL: %v", err)
	}

	// Agregar el query parameter con el TID
	q := u.Query()
	q.Set("tid", strconv.Itoa(tid)) // Usamos el TID convertido a string
	u.RawQuery = q.Encode()

	// Crear la solicitud HTTP
	req, err := http.NewRequest("POST", u.String(), nil)
	if err != nil {
		return "", fmt.Errorf("error al crear la request: %v", err)
	}

	// Establecer el Content-Type como JSON
	req.Header.Set("Content-Type", "application/json")

	// Enviar la solicitud HTTP
	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("error al enviar la request: %v", err)
	}
	defer resp.Body.Close()

	// Manejo de respuesta (si es necesario)
	return fmt.Sprintf("Request exitosa para el TID: %d", tid), nil
}

func MUTEX_CREATE(args []string) string {
	// Crear la URL con la dirección IP y el puerto
	url := fmt.Sprintf("http://%s:%d/mutexCreate", CpuConfigGlobal.IP_Kernel, CpuConfigGlobal.Port_Kernel)

	type Body struct {
		Name string `json:"Name"`
	}

	cuerpoRequest := Body{
		Name: args[0],
	}

	jsonBytes, err := json.Marshal(cuerpoRequest)
	if err != nil {
		return "error al convertir el struct a JSON"
	}

	// Crear la request POST
	req, err := http.NewRequest("POST", url, bytes.NewBuffer(jsonBytes))
	if err != nil {
		return "error al crear la request:"
	}

	// Establecer el Content-Type como JSON
	req.Header.Set("Content-Type", "application/json")

	// Hacer la request
	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		return "error al enviar la request: "
	}
	defer resp.Body.Close()
	return ""

}

type mutexToLock struct {
	RutaArchivo string
	SizeProceso int
	Prioridad   int
}

func MUTEX_LOCK(args []string) (string, error) {
	// Crear la URL con la dirección IP y el puerto
	url := fmt.Sprintf("http://%s:%d/mutexLock", CpuConfigGlobal.IP_Kernel, CpuConfigGlobal.Port_Kernel)
	type Body struct {
		Name string `json:"Name"`
	}

	cuerpoRequest := Body{
		Name: args[0],
	}

	jsonBytes, err := json.Marshal(cuerpoRequest)
	if err != nil {
		return "", fmt.Errorf("error al convertir el struct a JSON: %v", err)
	}

	// Crear la request POST
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
	return "", fmt.Errorf("error al enviar la request: %v", err)

}

func THREAD_EXIT(args []string) (string, error) {
	// Crear la URL con la dirección IP y el puerto
	baseURL := fmt.Sprintf("http://%s:%d/thread_exit", CpuConfigGlobal.IP_Kernel, CpuConfigGlobal.Port_Kernel)

	// Parsear la URL
	u, err := url.Parse(baseURL)
	if err != nil {
		return "", fmt.Errorf("error al parsear la URL: %v", err)
	}

	// Crear la request POST
	req, err := http.NewRequest("POST", u.String(), nil)
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

	// Manejo de respuesta (si es necesario)
	return "Request exitosa", nil
}

type mutexToUnlock struct {
	RutaArchivo string
	SizeProceso int
	Prioridad   int
}

func MUTEX_UNLOCK(args []string) (string, error) {
	// Crear la URL con la dirección IP y el puerto
	url := fmt.Sprintf("http://%s:%d/mutexUnlock", CpuConfigGlobal.IP_Kernel, CpuConfigGlobal.Port_Kernel)

	SizeProcesoInt, err := strconv.Atoi(args[1])
	if err != nil {
		return "", fmt.Errorf("error")
	}

	PrioridadInt, err := strconv.Atoi(args[1])
	if err != nil {
		return "", fmt.Errorf("error")
	}

	cuerpoRequest := processToCreate{
		RutaArchivo: args[0],
		SizeProceso: SizeProcesoInt,
		Prioridad:   PrioridadInt,
	}

	jsonBytes, err := json.Marshal(cuerpoRequest)
	if err != nil {
		return "", fmt.Errorf("error al convertir el struct a JSON: %v", err)
	}

	// Crear la request POST
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
	return "", fmt.Errorf("error al enviar la request: %v", err)

}

func PROCESS_EXIT(args []string) (string, error) {
	fmt.Println("IP KERNEL", CpuConfigGlobal.IP_Kernel)
	fmt.Println("IP PORT", CpuConfigGlobal.Port_Kernel)
	// Crear la URL con la dirección IP y el puerto
	baseURL := fmt.Sprintf("http://%s:%d/endProcess", CpuConfigGlobal.IP_Kernel, CpuConfigGlobal.Port_Kernel)

	// Parsear la URL
	u, err := url.Parse(baseURL)
	if err != nil {
		return "", fmt.Errorf("error al parsear la URL: %v", err)
	}

	// Crear la request POST
	req, err := http.NewRequest("POST", u.String(), nil)
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

	// Manejo de respuesta (si es necesario)
	return "Request exitosa", nil
}
