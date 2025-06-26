package bridge

import (
	"encoding/csv"
	"io"
	"log"
	"os"
	"strconv"
)

// ManagedCSVFile represents a CSV file that is managed for header consistency and appending data.
type ManagedCSVFile struct {
	filePath string
	header   []string
}

// slicesEqual is a helper function to check if two string slices are identical.
func slicesEqual(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i, v := range a {
		if v != b[i] {
			return false
		}
	}
	return true
}

// overwriteFileWithHeader truncates the file (or creates it) and writes the header.
func overwriteFileWithHeader(filePath string, header []string) error {
	// Open with O_TRUNC to clear the file or create it if it doesn't exist.
	file, err := os.OpenFile(filePath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0644)
	if err != nil {
		log.Printf("Error opening file %s for overwrite: %v", filePath, err)
		return err
	}
	defer file.Close()

	writer := csv.NewWriter(file)
	if err := writer.Write(header); err != nil {
		log.Printf("Error writing header to CSV file %s: %v", filePath, err)
		return err
	}
	writer.Flush()
	if err := writer.Error(); err != nil {
		log.Printf("Error flushing writer for %s: %v", filePath, err)
		return err
	}
	log.Printf("Header successfully written to %s by overwriteFileWithHeader.", filePath)
	return nil
}

// NewManagedCSV creates a new ManagedCSVFile instance.
// It initializes the CSV file at the given filePath with the specified header.
// If forceRecreate is true, any existing file at filePath will be deleted and recreated with the header.
// If forceRecreate is false:
//   - If the file doesn't exist, it's created with the header.
//   - If it exists but is empty or has an incorrect header, it's overwritten with the correct header.
//   - If it exists and has the correct header, it's left as is.
func NewManagedCSV(filePath string, header []string, forceRecreate bool) (*ManagedCSVFile, error) {
	if forceRecreate {
		log.Printf("Forcing recreation of CSV file %s with new header.", filePath)
		if err := overwriteFileWithHeader(filePath, header); err != nil {
			return nil, err
		}
	} else {
		// Try to open for read-write to check header. O_CREATE ensures it's made if not present.
		file, err := os.OpenFile(filePath, os.O_RDWR|os.O_CREATE, 0644)
		if err != nil {
			log.Printf("Error opening/creating CSV file %s for check: %v", filePath, err)
			return nil, err // Cannot proceed
		}

		reader := csv.NewReader(file)
		existingHeader, readErr := reader.Read()

		needsOverwrite := false
		if readErr == io.EOF { // File was created new or was empty
			log.Printf("File %s is new or empty. Writing header.", filePath)
			needsOverwrite = true
		} else if readErr != nil { // Other read error (e.g., malformed CSV, not just empty)
			log.Printf("Error reading header from CSV file %s (error: %v). Overwriting.", filePath, readErr)
			needsOverwrite = true
		} else if !slicesEqual(existingHeader, header) { // Header mismatch
			log.Printf("Header in %s is incorrect. Overwriting with new header.", filePath)
			needsOverwrite = true
		}

		if needsOverwrite {
			file.Close() // Close before overwriting
			if err := overwriteFileWithHeader(filePath, header); err != nil {
				return nil, err
			}
		} else {
			log.Printf("File %s already exists and has the correct header.", filePath)
			file.Close() // File is good, close it.
		}
	}

	return &ManagedCSVFile{
		filePath: filePath,
		header:   header,
	}, nil
}

// AppendData adds a data row to the CSV file.
func (m *ManagedCSVFile) AppendData(dataRow []string) error {
	// The constructor NewManagedCSV should have ensured the file exists and has a header.
	// We open in append mode. O_CREATE is a safeguard.
	file, err := os.OpenFile(m.filePath, os.O_APPEND|os.O_WRONLY|os.O_CREATE, 0644)
	if err != nil {
		log.Printf("Error opening CSV file %s in append mode: %v", m.filePath, err)
		return err
	}
	defer file.Close()

	writer := csv.NewWriter(file)

	err = writer.Write(dataRow)
	if err != nil {
		log.Printf("Error writing record to CSV %s: %v", m.filePath, err)
		return err
	}

	writer.Flush()       // Explicitly flush.
	err = writer.Error() // Check for errors from Write OR Flush.
	if err != nil {
		log.Printf("Error after flushing/writing to CSV %s: %v", m.filePath, err)
		return err
	}

	return nil
}

// AddDataRecord is an example of an exportable function that uses AppendDataToCSV.
// To pass specific data from Android, you might need to pass a JSON string
// and deserialize it here, or pass individual fields.
func AddDataRecord(filePath string, webSocketTime string, nopReceived int64, nopOriginal int64, lossRate float64) error {
	// Convert data to a slice of strings.
	// Define the header for this specific data structure.
	// In a real application, the header might be defined once globally or passed around.
	header := []string{"WebSocketTime", "nopReceived", "nopOriginal", "lossRate"}

	// Create or ensure the CSV file with the correct header.
	// forceRecreate: false - don't delete if it's already correct.
	// For multiple appends, it's more efficient to call NewManagedCSV once
	// and reuse the csvFile instance. This function demonstrates a single, self-contained operation.
	csvFile, err := NewManagedCSV(filePath, header, false)
	if err != nil {
		log.Printf("Error initializing managed CSV for AddDataRecord: %v", err)
		return err
	}

	record := []string{
		webSocketTime,
		strconv.FormatInt(nopReceived, 10),
		strconv.FormatInt(nopOriginal, 10),
		strconv.FormatFloat(lossRate, 'f', 4, 64), // 'f' for format, 4 decimal places, 64-bit float
	}
	return csvFile.AppendData(record)
}

