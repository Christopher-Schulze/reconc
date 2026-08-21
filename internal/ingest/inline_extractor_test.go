package ingest

import (
	"strings"
	"testing"
)

func TestExtractInlineBlocksTracksLinesAndCRLF(t *testing.T) {
	text := "prefix\r\n```reconc\r\nrules: []\r\n```\r\n\n```reconc \t\r\nrules: []\r\n```\r\n"
	blocks, err := extractInlineBlocks("AGENTS.md", text)
	if err != nil {
		t.Fatal(err)
	}
	if len(blocks) != 2 || blocks[0].LineStart != 2 || blocks[1].LineStart != 6 {
		t.Fatalf("blocks = %#v", blocks)
	}
	if blocks[0].Content != "rules: []\n" || blocks[1].Content != "rules: []\n" {
		t.Fatalf("contents = %#v", blocks)
	}
}

func TestExtractInlineBlocksSkipsUnclosedFenceAndRejectsIndentedClose(t *testing.T) {
	text := "```reconc\nrules: ignored\n  ```\n```reconc\nrules: kept\n```\n"
	blocks, err := extractInlineBlocks("AGENTS.md", text)
	if err != nil {
		t.Fatal(err)
	}
	if len(blocks) != 1 || !strings.Contains(blocks[0].Content, "rules: kept") {
		t.Fatalf("blocks = %#v", blocks)
	}
}

func TestExtractInlineBlocksEnforcesPerSourceCap(t *testing.T) {
	var builder strings.Builder
	for index := 0; index < maxInlineBlocksPerSource+1; index++ {
		builder.WriteString("```reconc\nrules: []\n```\n")
	}
	if _, err := extractInlineBlocks("AGENTS.md", builder.String()); err == nil || !strings.Contains(err.Error(), "exceeds") {
		t.Fatalf("block cap error = %v", err)
	}
}

func TestExtractInlineBlocksReturnsEmptyForPlainText(t *testing.T) {
	blocks, err := extractInlineBlocks("AGENTS.md", "plain\ntext\n")
	if err != nil || len(blocks) != 0 {
		t.Fatalf("plain extraction = %#v, %v", blocks, err)
	}
}
