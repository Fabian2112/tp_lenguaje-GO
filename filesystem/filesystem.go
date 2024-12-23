package main

import (
	"encoding/binary"
	"encoding/json"
	"errors"
	"filesystem/utils"
	"fmt"
	"log"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"time"
)

var config utils.FileSystemConfig
var Logger *slog.Logger

func main() {
	config = *utils.IniciarConfiguracionFileSystem("utils/config.json") // Usar la global
	mux := http.NewServeMux()
	InitFileSystem(config)

	mux.HandleFunc("/crearArchivoDump", crearArchivoDump)

	log.Printf("El FS está a la escucha en el puerto %d", config.Port)
	err := http.ListenAndServe(fmt.Sprintf(":%d", config.Port), mux)
	if err != nil {
		log.Fatalf("Error al iniciar el servidor: %v", err)
	}
}

func InitFileSystem(config utils.FileSystemConfig) {
	os.MkdirAll(filepath.Join(config.Mount_Dir, "files"), os.ModePerm)

	initFile(filepath.Join(config.Mount_Dir, "bitmap.dat"), config.Block_Count/8)
	initFile(filepath.Join(config.Mount_Dir, "bloques.dat"), config.Block_Count*config.Block_Size)
}

func initFile(path string, size int) {
	file, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0644)
	if err != nil {
		log.Fatalf("Error al abrir/crear archivo %s: %v", path, err)
	}
	defer file.Close()
	stat, _ := file.Stat()

	// Si el archivo tiene un tamaño diferente, reinicializarlo
	if stat.Size() != int64(size) {
		err := file.Truncate(int64(size))
		if err != nil {
			log.Fatalf("Error al truncar archivo %s: %v", path, err)
		}

		// Inicializar el contenido con ceros
		vacio := make([]byte, size)
		_, err = file.WriteAt(vacio, 0)
		if err != nil {
			log.Fatalf("Error al inicializar contenido de %s: %v", path, err)
		}
	}
}

func AccessBlock(nombreArchivo string, tipoBloque string, numeroBloqueFS int) {
	Logger.Info("## Acceso Bloque - Archivo: %s - Tipo Bloque: %s - Bloque File System %d", nombreArchivo, tipoBloque, numeroBloqueFS)
	time.Sleep(time.Duration(config.Block_Access_Delay))
}

// Función para crear el archivo de DUMP
func crearArchivoDump(w http.ResponseWriter, r *http.Request) {
	// Parsear los parámetros del request

	log.Println("hola")

	var requestData struct {
		Nombre    string `json:"nombre"`
		Tamano    int    `json:"tamano"`
		Contenido []byte `json:"contenido"`
	}

	err := json.NewDecoder(r.Body).Decode(&requestData)
	if err != nil {
		log.Printf("Error al decodificar el cuerpo de la solicitud: %v", err)
		http.Error(w, "Error al decodificar el cuerpo de la solicitud", http.StatusBadRequest)
		return
	}

	nombre := requestData.Nombre
	tamano := requestData.Tamano
	contenido := requestData.Contenido

	if nombre == "" || tamano == 0 {
		log.Printf("Parámetros incompletos: nombre=%s, tamano=%d", nombre, tamano)
		http.Error(w, "Parámetros incompletos", http.StatusBadRequest)
		return
	}

	numBloquesNecesarios := tamano / config.Block_Size
	if len(contenido)%config.Block_Size > 0 {
		numBloquesNecesarios++ // Calcular bloques necesarios
	}

	// Leer y verificar espacio en el bitmap
	rutaBitmap := filepath.Join(config.Mount_Dir, "bitmap.dat")
	archivoBitmap, err := os.OpenFile(rutaBitmap, os.O_RDWR, 0644)
	if err != nil {
		log.Printf("Error al abrir el archivo bitmap.dat: %v", err)
		http.Error(w, fmt.Sprintf("Error al abrir el archivo bitmap.dat: %v", err), http.StatusInternalServerError)
		return
	}
	defer archivoBitmap.Close()

	// Leer bitmap
	bitmap := make([]byte, (config.Block_Count+7)/8)
	_, err = archivoBitmap.Read(bitmap)
	if err != nil {
		log.Printf("Error al leer el bitmap: %v", err)
		http.Error(w, fmt.Sprintf("Error al leer el bitmap: %v", err), http.StatusInternalServerError)
		return
	}

	if !verificarEspacioDisponible(bitmap, numBloquesNecesarios+1) { // +1 para el bloque de índice
		log.Printf("No hay suficiente espacio disponible para crear el archivo DUMP")
		http.Error(w, "No hay suficiente espacio disponible para crear el archivo DUMP", http.StatusInsufficientStorage)
		return
	}

	// Reservar bloque de índice
	indexBlock, err := encontrarPrimerBloqueLibre(bitmap)
	if err != nil {
		log.Printf("Error al encontrar el bloque de índice: %v", err)
		http.Error(w, fmt.Sprintf("Error al encontrar el bloque de índice: %v", err), http.StatusInternalServerError)
		return
	}
	if err = reservarBloque(bitmap, indexBlock); err != nil {
		log.Printf("Error al reservar el bloque de índice: %v", err)
		http.Error(w, fmt.Sprintf("Error al reservar el bloque de índice: %v", err), http.StatusInternalServerError)
		return
	}
	Logger.Info("## Bloque asignado: %d - Archivo: %s - Bloques Libres: %d", indexBlock, nombre, verificarEspacioDisponible(bitmap, 0))

	// Reservar los bloques de datos
	bloquesDeDatos := make([]int, 0, numBloquesNecesarios)
	for i := 0; i < numBloquesNecesarios; i++ {
		bloque, err := encontrarPrimerBloqueLibre(bitmap)
		if err != nil {
			log.Printf("Error al encontrar un bloque libre: %v", err)
			http.Error(w, fmt.Sprintf("Error al encontrar un bloque libre: %v", err), http.StatusInternalServerError)
			return
		}
		if err = reservarBloque(bitmap, bloque); err != nil {
			log.Printf("Error al reservar el bloque de datos %d: %v", bloque, err)
			http.Error(w, fmt.Sprintf("Error al reservar el bloque de datos %d: %v", bloque, err), http.StatusInternalServerError)
			return
		}
		bloquesDeDatos = append(bloquesDeDatos, bloque)
		Logger.Info("## Bloque asignado: %d - Archivo: %s - Bloques Libres: %d", bloque, nombre, verificarEspacioDisponible(bitmap, 0))
	}

	// Guardar cambios en el bitmap
	err = guardarBitmap(rutaBitmap, bitmap)
	if err != nil {
		log.Printf("Error al guardar cambios en el bitmap: %v", err)
		http.Error(w, fmt.Sprintf("Error al guardar cambios en el bitmap: %v", err), http.StatusInternalServerError)
		return
	}

	// Imprimir el contenido del bitmap (para depuración)
	for i, byte := range bitmap {
		fmt.Printf("Byte %d: %08b\n", i, byte)
	}

	// Crear archivo de metadata
	err = crearMetadata(nombre, indexBlock, len(contenido))
	if err != nil {
		log.Printf("Error al crear el archivo de metadata: %v", err)
		http.Error(w, fmt.Sprintf("Error al crear el archivo de metadata: %v", err), http.StatusInternalServerError)
		return
	}

	// Escribir punteros en el bloque de índice
	rutaBloques := filepath.Join(config.Mount_Dir, "bloques.dat")
	err = escribirPunterosEnIndice(rutaBloques, indexBlock, bloquesDeDatos)
	if err != nil {
		log.Printf("Error al escribir punteros en el bloque de índice: %v", err)
		http.Error(w, fmt.Sprintf("Error al escribir punteros en el bloque de índice: %v", err), http.StatusInternalServerError)
		return
	}

	// Escribir datos en bloques
	err = escribirDatosEnBloques(rutaBloques, bloquesDeDatos, contenido, nombre)
	if err != nil {
		log.Printf("Error al escribir los datos en los bloques: %v", err)
		http.Error(w, fmt.Sprintf("Error al escribir los datos en los bloques: %v", err), http.StatusInternalServerError)
		return
	}

	Logger.Info("## Archivo Creado: %s - Tamaño: %d", nombre, tamano)
	Logger.Info("## Fin de solicitud - Archivo: %s", nombre)
	fmt.Fprintf(w, "Archivo DUMP '%s.dmp' creado exitosamente.\n", nombre)
}

func encontrarPrimerBloqueLibre(bitmap []byte) (int, error) {
	// Validar que el bitmap no esté vacío
	if len(bitmap) == 0 {
		return -1, fmt.Errorf("el bitmap está vacío")
	}

	// Iterar sobre cada byte y extraer el estado de cada bit
	for i, byte := range bitmap {
		for j := 0; j < 8; j++ {
			// Verificar si el bit está apagado (0), lo que indica un bloque libre
			if byte&(1<<uint(7-j)) == 0 {
				// Calcular la posición global del bloque
				posicionBloque := i*8 + j
				return posicionBloque, nil
			}
		}
	}

	// Si no se encontró ningún bloque libre, retornar -1
	return -1, fmt.Errorf("no se encontraron bloques libres en el bitmap")
}

// Función para verificar si hay suficiente espacio disponible
func verificarEspacioDisponible(bitmap []byte, numBloquesNecesarios int) bool {
	// Contar bloques libres en el bitmap
	contarBloquesLibres := 0
	for _, byteBitmap := range bitmap {
		for i := 0; i < 8; i++ {
			if byteBitmap&(1<<uint(7-i)) == 0 {
				contarBloquesLibres++
			}
		}
	}
	// Verificar si hay suficientes bloques libres
	return contarBloquesLibres >= numBloquesNecesarios
}

// Función para reservar un bloque en el bitmap
func reservarBloque(bitmap []byte, indiceBloque int) error {
	// Verificar que el índice del bloque esté dentro del rango válido
	if indiceBloque < 0 || indiceBloque >= len(bitmap)*8 {
		return errors.New("índice de bloque fuera de rango")
	}

	// Calcular el índice del byte y el índice del bit
	byteIndice := indiceBloque / 8
	bitIndice := indiceBloque % 8

	// Si el bit ya está ocupado, devolver un error
	if bitmap[byteIndice]&(1<<uint(7-bitIndice)) != 0 {
		return fmt.Errorf("el bloque %d ya está ocupado", indiceBloque)
	}

	// Marcar el bit como ocupado
	bitmap[byteIndice] |= (1 << uint(7-bitIndice))
	return nil
}

// Función para crear un archivo de metadata
func crearMetadata(nombre string, indexBlock, size int) error {
	// Crear archivo de metadata
	metadataFile, err := os.Create(nombre)
	if err != nil {
		return fmt.Errorf("error al crear el archivo de metadata: %v", err)
	}
	defer metadataFile.Close()

	// Escribir la metadata
	metadataContent := fmt.Sprintf("{\n\t\"index_block\": %d,\n\t\"size\": %d\n}\n", indexBlock, size)
	_, err = metadataFile.WriteString(metadataContent)
	if err != nil {
		return fmt.Errorf("error al escribir la metadata en el archivo: %v", err)
	}

	return nil
}

func escribirPunterosEnIndice(bloquesDatPath string, indexBlock int, punteros []int) error {
	file, err := os.OpenFile(bloquesDatPath, os.O_RDWR, 0644)
	if err != nil {
		return fmt.Errorf("no se pudo abrir el archivo bloques.dat: %v", err)
	}
	defer file.Close()

	// Calcular la posición del bloque de índice en el archivo
	posicion := indexBlock * config.Block_Size

	// Mover el cursor a la posición del bloque de índice
	_, err = file.Seek(int64(posicion), 0)
	if err != nil {
		return fmt.Errorf("error al mover el cursor al bloque de índice: %v", err)
	}

	// Escribir los punteros
	for _, puntero := range punteros {
		punteroBytes := make([]byte, 4)
		binary.BigEndian.PutUint32(punteroBytes, uint32(puntero))
		_, err = file.Write(punteroBytes)
		if err != nil {
			return fmt.Errorf("error al escribir puntero en el bloque de índice: %v", err)
		}
	}

	return nil
}

func escribirDatosEnBloques(bloquesDatPath string, bloques []int, contenido []byte, nombreArchivo string) error {
	file, err := os.OpenFile(bloquesDatPath, os.O_RDWR, 0644)
	if err != nil {
		return fmt.Errorf("no se pudo abrir el archivo bloques.dat: %v", err)
	}
	defer file.Close()

	bloqueSize := config.Block_Size
	contenidoLen := len(contenido) // Longitud del contenido

	for i, bloque := range bloques {
		// Calcular la posición del bloque en el archivo
		posicion := bloque * bloqueSize
		_, err = file.Seek(int64(posicion), 0)
		if err != nil {
			return fmt.Errorf("error al mover el cursor al bloque %d: %v", bloque, err)
		}

		// Calcular los índices de inicio y fin para el bloque
		inicio := i * bloqueSize
		fin := inicio + bloqueSize

		// Asegurarse de que el índice de fin no supere la longitud del contenido
		if fin > contenidoLen {
			fin = contenidoLen
		}

		// Asegurarse de que el índice de inicio no supere la longitud del contenido
		if inicio > contenidoLen {
			return fmt.Errorf("índice de inicio fuera de rango para el bloque %d", bloque)
		}

		// Imprimir los valores para depuración
		fmt.Printf("Bloque: %d, Inicio: %d, Fin: %d, Longitud del contenido: %d\n", bloque, inicio, fin, contenidoLen)

		// Escribir datos del bloque
		_, err = file.Write(contenido[inicio:fin])
		if err != nil {
			return fmt.Errorf("error al escribir datos en el bloque %d: %v", bloque, err)
		}

		// Simular latencia
		AccessBlock(nombreArchivo, "DATOS", bloque)
	}

	return nil
}

func guardarBitmap(rutaBitmap string, bitmap []byte) error {
	archivo, err := os.OpenFile(rutaBitmap, os.O_WRONLY, 0644)
	if err != nil {
		return fmt.Errorf("error al abrir archivo bitmap.dat para escribir: %v", err)
	}
	defer archivo.Close()

	_, err = archivo.WriteAt(bitmap, 0)
	if err != nil {
		return fmt.Errorf("error al guardar cambios en bitmap: %v", err)
	}
	return nil
}
