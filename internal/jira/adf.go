package jira

import (
	"bytes"
	"encoding/json"
	"strings"
)

type adfNode map[string]json.RawMessage

func (n adfNode) kind() string {
	var kind string
	_ = json.Unmarshal(n["type"], &kind)
	return kind
}

func (n adfNode) stringValue(name string) string {
	var value string
	_ = json.Unmarshal(n[name], &value)
	return value
}

func (n adfNode) children() []adfNode {
	var content []adfNode
	_ = json.Unmarshal(n["content"], &content)
	return content
}

// ADFText returns a plain-text preview and whether it can safely be edited
// as plain paragraphs. Unknown nodes, marks and attributes stay Jira-only.
func ADFText(raw json.RawMessage) (text string, editable bool) {
	if len(raw) == 0 || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return "", true
	}
	var root adfNode
	if json.Unmarshal(raw, &root) != nil || root.kind() != "doc" {
		return "", false
	}
	var preview strings.Builder
	writeADFPreview(&preview, root, 0)
	editable = len(root) == 3 && string(root["version"]) == "1" && plainADFDocument(root)
	return strings.TrimSuffix(preview.String(), "\n"), editable
}

func plainADFDocument(root adfNode) bool {
	var paragraphs []adfNode
	if json.Unmarshal(root["content"], &paragraphs) != nil {
		return false
	}
	for _, paragraph := range paragraphs {
		if paragraph.kind() != "paragraph" || len(paragraph) > 2 {
			return false
		}
		if len(paragraph) == 1 {
			continue
		}
		var inline []adfNode
		if json.Unmarshal(paragraph["content"], &inline) != nil {
			return false
		}
		for _, node := range inline {
			if node.kind() != "text" || len(node) != 2 || node.stringValue("text") == "" ||
				strings.ContainsAny(node.stringValue("text"), "\r\n") {
				return false
			}
		}
	}
	return true
}

func writeADFPreview(out *strings.Builder, n adfNode, depth int) {
	if depth > 64 {
		return
	}
	switch n.kind() {
	case "text":
		out.WriteString(n.stringValue("text"))
	case "hardBreak":
		out.WriteByte('\n')
	default:
		for _, child := range n.children() {
			writeADFPreview(out, child, depth+1)
		}
		switch n.kind() {
		case "paragraph", "heading", "codeBlock":
			out.WriteByte('\n')
		}
	}
}

// PlainADF builds the REST v3 document used by the panel's plain-text inputs.
func PlainADF(text string) json.RawMessage {
	type node struct {
		Type    string `json:"type"`
		Text    string `json:"text,omitempty"`
		Content []node `json:"content,omitempty"`
		Version int    `json:"version,omitempty"`
	}
	text = strings.ReplaceAll(strings.ReplaceAll(text, "\r\n", "\n"), "\r", "\n")
	doc := node{Type: "doc", Version: 1}
	for _, line := range strings.Split(text, "\n") {
		paragraph := node{Type: "paragraph"}
		if line != "" {
			paragraph.Content = []node{{Type: "text", Text: line}}
		}
		doc.Content = append(doc.Content, paragraph)
	}
	raw, _ := json.Marshal(doc)
	return raw
}
