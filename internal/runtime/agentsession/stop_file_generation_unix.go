//go:build !windows

package agentsession

import (
	"fmt"
	"os"
	"reflect"
)

func platformFileGeneration(_ string, info os.FileInfo) (string, bool) {
	value := reflect.ValueOf(info.Sys())
	if !value.IsValid() {
		return "", false
	}
	if value.Kind() == reflect.Pointer {
		if value.IsNil() {
			return "", false
		}
		value = value.Elem()
	}
	if value.Kind() != reflect.Struct {
		return "", false
	}
	device, deviceOK := integerStructField(value, "Dev")
	inode, inodeOK := integerStructField(value, "Ino")
	change, changeOK := timespecStructField(value, "Ctim", "Ctimespec")
	if !deviceOK || !inodeOK || !changeOK {
		return "", false
	}
	return fmt.Sprintf("dev=%s;ino=%s;change=%s", device, inode, change), true
}

func integerStructField(value reflect.Value, name string) (string, bool) {
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

func timespecStructField(value reflect.Value, names ...string) (string, bool) {
	for _, name := range names {
		field := value.FieldByName(name)
		if !field.IsValid() {
			continue
		}
		if field.Kind() == reflect.Pointer {
			if field.IsNil() {
				continue
			}
			field = field.Elem()
		}
		if field.Kind() != reflect.Struct {
			continue
		}
		seconds, secondsOK := integerStructField(field, "Sec")
		nanos, nanosOK := integerStructField(field, "Nsec")
		if secondsOK && nanosOK {
			return seconds + ":" + nanos, true
		}
	}
	return "", false
}
