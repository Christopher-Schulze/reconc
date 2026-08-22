//go:build !windows

package actionledger

import (
	"fmt"
	"os"
	"reflect"
)

func ledgerFileGeneration(path string) (string, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return "", err
	}
	value := reflect.ValueOf(info.Sys())
	if value.Kind() == reflect.Pointer && !value.IsNil() {
		value = value.Elem()
	}
	if value.Kind() != reflect.Struct {
		return "", fmt.Errorf("file generation is unavailable")
	}
	integer := func(name string) (string, bool) {
		field := value.FieldByName(name)
		if !field.IsValid() {
			return "", false
		}
		switch field.Kind() {
		case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
			return fmt.Sprintf("%d", field.Int()), true
		case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
			return fmt.Sprintf("%d", field.Uint()), true
		default:
			return "", false
		}
	}
	device, deviceOK := integer("Dev")
	inode, inodeOK := integer("Ino")
	var change string
	for _, name := range []string{"Ctim", "Ctimespec"} {
		field := value.FieldByName(name)
		if !field.IsValid() || field.Kind() != reflect.Struct {
			continue
		}
		seconds := field.FieldByName("Sec")
		nanos := field.FieldByName("Nsec")
		if seconds.IsValid() && nanos.IsValid() {
			change = fmt.Sprintf("%d:%d", seconds.Int(), nanos.Int())
			break
		}
	}
	if !deviceOK || !inodeOK || change == "" {
		return "", fmt.Errorf("reliable file change generation is unavailable")
	}
	return "dev=" + device + ";ino=" + inode + ";change=" + change, nil
}
