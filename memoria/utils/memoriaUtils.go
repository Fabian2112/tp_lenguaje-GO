package utils

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
)

type MemoriaConfig struct {
	Port              int    `json:"port"`
	Memory_Size       int    `json:"memory_size"`
	Instructions_Path string `json:"instructions_path"`
	Response_Delay    int    `json:"response_delay"`
	IP_Kernel         string `json:"ip_kernel"`
	Port_Kernel       int    `json:"port_kernel"`
	IP_CPU            string `json:"ip_cpu"`
	Port_CPU          int    `json:"port_cpu"`
	IP_Filesystem     string `json:"ip_filesystem"`
	Port_Filesystem   int    `json:"port_filesystem"`
	Scheme            string `json:"scheme"`
	Search_algorithm  string `json:"search_algorithm"`
	Partitions        []int  `json:"partitions"`
	Log_Level         int    `json:"log_level"`
}

func IniciarConfiguracionMemoria(config *MemoriaConfig, filePath string) {
	configFile, err := os.Open(filePath)
	if err != nil {
		log.Fatal(err.Error())
	}
	defer configFile.Close()

	jsonParser := json.NewDecoder(configFile)
	jsonParser.Decode(&config)

}

func MostrarMemoriaConfig(config MemoriaConfig) {
	fmt.Println("Detalles de MemoriaConfig:")
	fmt.Printf("Port: %d\n", config.Port)
	fmt.Printf("Memory Size: %d\n", config.Memory_Size)
	fmt.Printf("Instructions Path: %s\n", config.Instructions_Path)
	fmt.Printf("Delay Response: %d\n", config.Response_Delay)
	fmt.Printf("IP Kernel: %s\n", config.IP_Kernel)
	fmt.Printf("Port Kernel: %d\n", config.Port_Kernel)
	fmt.Printf("IP CPU: %s\n", config.IP_CPU)
	fmt.Printf("Port CPU: %d\n", config.Port_CPU)
	fmt.Printf("IP Filesystem: %s\n", config.IP_Filesystem)
	fmt.Printf("Port Filesystem: %d\n", config.Port_Filesystem)
	fmt.Printf("Scheme: %s\n", config.Scheme)
	fmt.Printf("Search Algorithm: %s\n", config.Search_algorithm)
	fmt.Printf("Partitions: %v\n", config.Partitions)
	fmt.Printf("Log Level: %d\n", config.Log_Level)
}

// Memoria de Sistema

type EspacioMemoria struct {
	Size   int
	Base   int
	Limit  int
	IsFree bool
	PID    int
}

type ContextoEjeccion struct {
	Base          int // Dirección base
	Limit         int // Límite de la dirección
	RegistroHilos RegistroHilos
}

// Contexto de ejecución por proceso
type ProcessContext struct {
	Base  int // Dirección base
	Limit int // Límite de la dirección
}

// Registros de la CPU por hilo
type ThreadContext struct {
	TID           int // Identificador del hilo
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
