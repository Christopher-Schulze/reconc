package main

import (
	"bytes"
	"errors"
	"fmt"
	"strings"
)

func renderNotices(inventory noticeInventory) ([]byte, error) {
	if len(inventory.Targets) == 0 || len(inventory.Components) == 0 {
		return nil, errors.New("notice inventory is empty")
	}
	var output bytes.Buffer
	output.WriteString("RECONC THIRD-PARTY LICENSE NOTICES\n\n")
	output.WriteString("This file is generated from the exact Go dependency graph used by the release targets.\n")
	fmt.Fprintf(&output, "Release targets: %s\n", strings.Join(inventory.Targets, ", "))
	for _, component := range inventory.Components {
		if component.Identity == "" || len(component.Files) == 0 {
			return nil, errors.New("notice component is incomplete")
		}
		output.WriteString("\n================================================================================\n")
		fmt.Fprintf(&output, "Component: %s\n", component.Identity)
		for _, file := range component.Files {
			if file.Name == "" || len(file.Digest) != 64 || len(file.Body) == 0 {
				return nil, fmt.Errorf("notice file for %s is incomplete", component.Identity)
			}
			output.WriteString("--------------------------------------------------------------------------------\n")
			fmt.Fprintf(&output, "File: %s\nSHA-256: %s\n\n", file.Name, file.Digest)
			output.Write(file.Body)
			if file.Body[len(file.Body)-1] != '\n' {
				output.WriteByte('\n')
			}
		}
	}
	if output.Len() > maxNoticeBytes {
		return nil, fmt.Errorf("rendered license notices exceed %d bytes", maxNoticeBytes)
	}
	return output.Bytes(), nil
}
