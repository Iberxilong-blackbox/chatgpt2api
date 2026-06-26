package main

import (
	"archive/zip"
	"encoding/json"
	"encoding/xml"
	"flag"
	"fmt"
	"io"
	"os"
	"path"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"chatgpt2api/internal/service"
	"chatgpt2api/internal/storage"
)

func main() {
	xlsxPath := flag.String("xlsx", "", "path to the source .xlsx file")
	outPath := flag.String("out", "registration_identity_ids.json", "path to write registration_identity_ids.json")
	existingPath := flag.String("existing", "", "optional existing registration_identity_ids.json to merge")
	databaseURL := flag.String("database-url", "", "optional database URL; writes registration_identity_ids.json into json_documents")
	sheetName := flag.String("sheet", "", "optional worksheet name; defaults to the first sheet")
	columnName := flag.String("column", "A", "worksheet column containing identity_id values")
	skipHeader := flag.Bool("skip-header", false, "skip the first non-empty identity_id row")
	label := flag.String("label", "", "optional default label for newly added identity IDs")
	flag.Parse()

	if strings.TrimSpace(*xlsxPath) == "" {
		exitf("-xlsx is required")
	}
	ids, err := readXLSXColumn(*xlsxPath, *sheetName, *columnName)
	if err != nil {
		exitf("read xlsx: %v", err)
	}
	if *skipHeader && len(ids) > 0 {
		ids = ids[1:]
	}
	duplicateDetails := duplicateIdentityIDs(ids)

	var existing any
	if strings.TrimSpace(*existingPath) != "" {
		data, err := os.ReadFile(*existingPath)
		if err != nil {
			exitf("read existing json: %v", err)
		}
		if len(strings.TrimSpace(string(data))) > 0 {
			if err := json.Unmarshal(data, &existing); err != nil {
				exitf("parse existing json: %v", err)
			}
		}
	}

	doc, stats, err := service.MergeRegistrationIdentityIDs(existing, ids, *label)
	if err != nil {
		exitf("merge identity IDs: %v", err)
	}
	data, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		exitf("encode output json: %v", err)
	}
	if err := os.WriteFile(*outPath, append(data, '\n'), 0o600); err != nil {
		exitf("write output json: %v", err)
	}
	wrote := *outPath
	if strings.TrimSpace(*databaseURL) != "" {
		backend, err := storage.NewDatabaseBackend(*databaseURL)
		if err != nil {
			exitf("open database: %v", err)
		}
		defer backend.Close()
		if err := backend.SaveJSONDocument("registration_identity_ids.json", doc); err != nil {
			exitf("write database document: %v", err)
		}
		wrote += " and database document registration_identity_ids.json"
	}
	fmt.Printf("wrote %s: input=%d existing=%d added=%d duplicates=%d\n", wrote, stats.Input, stats.Existing, stats.Added, stats.Duplicates)
	if len(duplicateDetails) > 0 {
		fmt.Println("duplicate identity_id values in xlsx:")
		for _, item := range duplicateDetails {
			fmt.Printf("- %q total=%d duplicate_rows=%d\n", item.Value, item.Total, item.DuplicateRows)
		}
	}
}

func exitf(format string, args ...any) {
	fmt.Fprintf(os.Stderr, format+"\n", args...)
	os.Exit(1)
}

type duplicateIdentityID struct {
	Value         string
	Total         int
	DuplicateRows int
}

func duplicateIdentityIDs(ids []string) []duplicateIdentityID {
	counts := map[string]int{}
	for _, id := range ids {
		value := strings.TrimSpace(id)
		if value == "" {
			continue
		}
		counts[value]++
	}
	out := make([]duplicateIdentityID, 0)
	for value, total := range counts {
		if total < 2 {
			continue
		}
		out = append(out, duplicateIdentityID{Value: value, Total: total, DuplicateRows: total - 1})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].DuplicateRows != out[j].DuplicateRows {
			return out[i].DuplicateRows > out[j].DuplicateRows
		}
		return out[i].Value < out[j].Value
	})
	return out
}
func readXLSXColumn(filename, sheetName, column string) ([]string, error) {
	column = strings.ToUpper(strings.TrimSpace(column))
	if !regexp.MustCompile(`^[A-Z]+$`).MatchString(column) {
		return nil, fmt.Errorf("invalid column %q", column)
	}
	reader, err := zip.OpenReader(filename)
	if err != nil {
		return nil, err
	}
	defer reader.Close()

	files := map[string]*zip.File{}
	for _, file := range reader.File {
		files[file.Name] = file
	}
	sharedStrings, err := readSharedStrings(files)
	if err != nil {
		return nil, err
	}
	sheetPath, err := resolveSheetPath(files, sheetName)
	if err != nil {
		return nil, err
	}
	return readSheetColumn(files[sheetPath], column, sharedStrings)
}

func readSharedStrings(files map[string]*zip.File) ([]string, error) {
	file := files["xl/sharedStrings.xml"]
	if file == nil {
		return nil, nil
	}
	rc, err := file.Open()
	if err != nil {
		return nil, err
	}
	defer rc.Close()
	decoder := xml.NewDecoder(rc)
	var values []string
	for {
		token, err := decoder.Token()
		if err == io.EOF {
			return values, nil
		}
		if err != nil {
			return nil, err
		}
		start, ok := token.(xml.StartElement)
		if !ok || start.Name.Local != "si" {
			continue
		}
		var item sharedStringItem
		if err := decoder.DecodeElement(&item, &start); err != nil {
			return nil, err
		}
		values = append(values, strings.Join(item.Texts, ""))
	}
}

type sharedStringItem struct {
	Texts []string `xml:"t"`
}

func resolveSheetPath(files map[string]*zip.File, sheetName string) (string, error) {
	workbook := files["xl/workbook.xml"]
	if workbook == nil {
		return "", fmt.Errorf("xl/workbook.xml not found")
	}
	relationships, err := readWorkbookRelationships(files)
	if err != nil {
		return "", err
	}
	rc, err := workbook.Open()
	if err != nil {
		return "", err
	}
	defer rc.Close()
	decoder := xml.NewDecoder(rc)
	for {
		token, err := decoder.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return "", err
		}
		start, ok := token.(xml.StartElement)
		if !ok || start.Name.Local != "sheet" {
			continue
		}
		name := attr(start, "name")
		if strings.TrimSpace(sheetName) != "" && name != sheetName {
			continue
		}
		id := attrByLocal(start, "id")
		target := relationships[id]
		if target == "" {
			return "", fmt.Errorf("relationship %q for sheet %q not found", id, name)
		}
		sheetPath := path.Clean("xl/" + target)
		if strings.HasPrefix(target, "/") {
			sheetPath = strings.TrimPrefix(path.Clean(target), "/")
		}
		if files[sheetPath] == nil {
			return "", fmt.Errorf("sheet file %q not found", sheetPath)
		}
		return sheetPath, nil
	}
	if strings.TrimSpace(sheetName) != "" {
		return "", fmt.Errorf("sheet %q not found", sheetName)
	}
	return "", fmt.Errorf("workbook contains no sheets")
}

func readWorkbookRelationships(files map[string]*zip.File) (map[string]string, error) {
	file := files["xl/_rels/workbook.xml.rels"]
	if file == nil {
		return nil, fmt.Errorf("xl/_rels/workbook.xml.rels not found")
	}
	rc, err := file.Open()
	if err != nil {
		return nil, err
	}
	defer rc.Close()
	var rels struct {
		Items []struct {
			ID     string `xml:"Id,attr"`
			Target string `xml:"Target,attr"`
		} `xml:"Relationship"`
	}
	if err := xml.NewDecoder(rc).Decode(&rels); err != nil {
		return nil, err
	}
	out := map[string]string{}
	for _, rel := range rels.Items {
		out[rel.ID] = rel.Target
	}
	return out, nil
}

func readSheetColumn(file *zip.File, column string, sharedStrings []string) ([]string, error) {
	rc, err := file.Open()
	if err != nil {
		return nil, err
	}
	defer rc.Close()
	decoder := xml.NewDecoder(rc)
	var values []string
	nextColumnIndex := 0
	for {
		token, err := decoder.Token()
		if err == io.EOF {
			return values, nil
		}
		if err != nil {
			return nil, err
		}
		start, ok := token.(xml.StartElement)
		if !ok {
			continue
		}
		if start.Name.Local == "row" {
			nextColumnIndex = 0
			continue
		}
		if start.Name.Local != "c" {
			continue
		}
		cellRef := attr(start, "r")
		cellColumn := columnFromCellRef(cellRef)
		if cellColumn == "" {
			cellColumn = columnFromIndex(nextColumnIndex)
		}
		nextColumnIndex++
		var cell xlsxCell
		if err := decoder.DecodeElement(&cell, &start); err != nil {
			return nil, err
		}
		if cellColumn != column {
			continue
		}
		value := strings.TrimSpace(cellValue(cell, sharedStrings))
		if value != "" {
			values = append(values, value)
		}
	}
}

type xlsxCell struct {
	Type        string `xml:"t,attr"`
	Value       string `xml:"v"`
	InlineValue string `xml:"is>t"`
}

func cellValue(cell xlsxCell, sharedStrings []string) string {
	switch cell.Type {
	case "s":
		index, err := strconv.Atoi(strings.TrimSpace(cell.Value))
		if err == nil && index >= 0 && index < len(sharedStrings) {
			return sharedStrings[index]
		}
	case "inlineStr":
		return cell.InlineValue
	}
	return cell.Value
}

func attr(start xml.StartElement, name string) string {
	for _, item := range start.Attr {
		if item.Name.Local == name && item.Name.Space == "" {
			return item.Value
		}
	}
	return ""
}

func attrByLocal(start xml.StartElement, local string) string {
	for _, item := range start.Attr {
		if item.Name.Local == local {
			return item.Value
		}
	}
	return ""
}

func columnFromCellRef(ref string) string {
	var out strings.Builder
	for _, r := range ref {
		if r >= 'A' && r <= 'Z' || r >= 'a' && r <= 'z' {
			out.WriteRune(r)
			continue
		}
		break
	}
	return strings.ToUpper(out.String())
}

func columnFromIndex(index int) string {
	index++
	var out []byte
	for index > 0 {
		index--
		out = append([]byte{byte('A' + index%26)}, out...)
		index /= 26
	}
	return string(out)
}
