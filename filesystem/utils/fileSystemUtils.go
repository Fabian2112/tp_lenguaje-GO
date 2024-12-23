package utils

import (
	"encoding/json"
	"log"
	"os"
)

type FileSystemConfig struct {
	Port               int    `json:"port"`
	Ip_Memory          string `json:"ip_memory"`
	Port_Memory        int    `json:"port_memory"`
	Mount_Dir          string `json:"mount_dir"`
	Block_Size         int    `json:"block_size"`
	Block_Count        int    `json:"block_count"`
	Block_Access_Delay int    `json:"block_access_delay"`
	Log_Level          string `json:"log_level"`
}

func IniciarConfiguracionFileSystem(filePath string) *FileSystemConfig {
	var config *FileSystemConfig
	configFile, err := os.Open(filePath)
	if err != nil {
		log.Fatal(err.Error())
	}
	defer configFile.Close()

	jsonParser := json.NewDecoder(configFile)
	jsonParser.Decode(&config)

	return config
}
