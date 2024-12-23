package utils

import (
	"encoding/json"
	"log"
	"os"
)

type KernelConfig struct {
	Port                int      `json:"port"`
	IP_Memoria          string `json:"ip_memory"`
	Port_Memoria        int    `json:"port_memory"`
	IP_CPU              string `json:"ip_cpu"`
	Port_CPU            int    `json:"port_cpu"`
	Scheduler_Algorithm string `json:"sheduler_algorithm"`
	Quantum             int    `json:"quantum"`
	Log_Level           int    `json:"log_level"`
	Multiprogramming    int      `json:"multiprogramming"`
}

func IniciarConfiguracionKernel(filePath string) *KernelConfig {
	var config *KernelConfig
	configFile, err := os.Open(filePath)
	if err != nil {
		log.Fatal(err.Error())
	}
	defer configFile.Close()

	jsonParser := json.NewDecoder(configFile)
	jsonParser.Decode(&config)

	return config
}
