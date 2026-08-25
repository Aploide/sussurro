package main

import (
	"bytes"
	"go/ast"
	"go/format"
	"go/parser"
	"go/token"
	"reflect"
	"testing"
)

type recordingTranscriber struct {
	events     []string
	dictionary []string
}

func (r *recordingTranscriber) SetDictionary(dictionary []string) {
	r.events = append(r.events, "dictionary")
	r.dictionary = append([]string(nil), dictionary...)
}

func (r *recordingTranscriber) Transcribe([]float32) (string, error) {
	r.events = append(r.events, "transcribe")
	return "text", nil
}

func TestTranscribeWithDictionaryPrimesBeforeRecognition(t *testing.T) {
	engine := &recordingTranscriber{}
	dictionary := []string{"Sussurro", "Kubernetes"}
	got, err := transcribeWithDictionary(engine, dictionary, []float32{0.1})
	if err != nil {
		t.Fatal(err)
	}
	if got != "text" {
		t.Errorf("transcribeWithDictionary() = %q, want text", got)
	}
	if !reflect.DeepEqual(engine.dictionary, dictionary) {
		t.Errorf("dictionary = %#v, want %#v", engine.dictionary, dictionary)
	}
	if !reflect.DeepEqual(engine.events, []string{"dictionary", "transcribe"}) {
		t.Errorf("events = %#v, want dictionary before transcribe", engine.events)
	}
}

func TestMainUsesDictionaryTranscriptionPath(t *testing.T) {
	files := token.NewFileSet()
	file, err := parser.ParseFile(files, "main.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	for _, declaration := range file.Decls {
		fn, ok := declaration.(*ast.FuncDecl)
		if !ok || fn.Name.Name != "main" {
			continue
		}
		transcribeAt, cleanupAt := -1, -1
		for index, statement := range fn.Body.List {
			if conditional, ok := statement.(*ast.IfStmt); ok && nodeText(t, files, conditional.Cond) == "*clean" {
				cleanupAt = index
			}
			assignment, ok := statement.(*ast.AssignStmt)
			if !ok || len(assignment.Lhs) != 2 || len(assignment.Rhs) != 1 {
				continue
			}
			call, ok := assignment.Rhs[0].(*ast.CallExpr)
			if !ok || nodeText(t, files, call.Fun) != "transcribeWithDictionary" {
				continue
			}
			if nodeText(t, files, assignment.Lhs[0]) != "text" || nodeText(t, files, assignment.Lhs[1]) != "err" {
				t.Fatal("dictionary transcription result is not assigned to text and err")
			}
			if len(call.Args) != 3 || nodeText(t, files, call.Args[0]) != "asrEngine" ||
				nodeText(t, files, call.Args[1]) != "cfg.App.Dictionary" || nodeText(t, files, call.Args[2]) != "samples" {
				t.Fatal("dictionary transcription call does not use the ASR engine, configured dictionary, and decoded samples")
			}
			transcribeAt = index
		}
		if transcribeAt < 0 {
			t.Fatal("main does not call transcribeWithDictionary directly")
		}
		if cleanupAt < 0 || transcribeAt > cleanupAt {
			t.Fatal("dictionary transcription must run before optional cleanup")
		}
		return
	}
	t.Fatal("main function not found")
}

func nodeText(t *testing.T, files *token.FileSet, node ast.Node) string {
	t.Helper()
	var output bytes.Buffer
	if err := format.Node(&output, files, node); err != nil {
		t.Fatal(err)
	}
	return output.String()
}
