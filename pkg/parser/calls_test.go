package parser

import (
	"reflect"
	"testing"
)

func TestParse_CallsExtraction(t *testing.T) {
	dispatcher := NewDispatcher()

	t.Run("Go calls extraction", func(t *testing.T) {
		code := `package main

import "fmt"

func Helper() {
	fmt.Println("hello")
}

func Process() {
	Helper()
	compute(123)
}
`
		res, err := dispatcher.Parse("cmd/main.go", []byte(code))
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(res.Symbols) != 2 {
			t.Fatalf("expected 2 symbols, got %d", len(res.Symbols))
		}

		// Helper calls Println (fmt is selector, Println is call)
		helperSym := res.Symbols[0]
		if helperSym.Name != "Helper" {
			t.Errorf("expected Helper, got %s", helperSym.Name)
		}
		expectedHelperCalls := []string{"Println"}
		if !reflect.DeepEqual(helperSym.Calls, expectedHelperCalls) {
			t.Errorf("expected Helper calls %v, got %v", expectedHelperCalls, helperSym.Calls)
		}

		// Process calls Helper, compute
		processSym := res.Symbols[1]
		if processSym.Name != "Process" {
			t.Errorf("expected Process, got %s", processSym.Name)
		}
		expectedProcessCalls := []string{"Helper", "compute"}
		if !reflect.DeepEqual(processSym.Calls, expectedProcessCalls) {
			t.Errorf("expected Process calls %v, got %v", expectedProcessCalls, processSym.Calls)
		}
	})

	t.Run("Python calls extraction", func(t *testing.T) {
		code := `def start():
    authenticate()
    return _verify_hash()

def authenticate():
    pass
`
		res, err := dispatcher.Parse("service/app.py", []byte(code))
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(res.Symbols) != 2 {
			t.Fatalf("expected 2 symbols, got %d", len(res.Symbols))
		}
		startSym := res.Symbols[0]
		expectedCalls := []string{"authenticate", "_verify_hash"}
		if !reflect.DeepEqual(startSym.Calls, expectedCalls) {
			t.Errorf("expected start calls %v, got %v", expectedCalls, startSym.Calls)
		}
	})

	t.Run("TypeScript calls extraction", func(t *testing.T) {
		code := `export function handleLogin() {
    login();
    validateToken();
}

function login() {
}
`
		res, err := dispatcher.Parse("src/app.ts", []byte(code))
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(res.Symbols) != 2 {
			t.Fatalf("expected 2 symbols, got %d", len(res.Symbols))
		}
		handleLoginSym := res.Symbols[0]
		expectedCalls := []string{"login", "validateToken"}
		if !reflect.DeepEqual(handleLoginSym.Calls, expectedCalls) {
			t.Errorf("expected handleLogin calls %v, got %v", expectedCalls, handleLoginSym.Calls)
		}
	})

	t.Run("Rust calls extraction", func(t *testing.T) {
		code := `pub fn execute() {
    init_db();
    run_worker();
}
`
		res, err := dispatcher.Parse("src/main.rs", []byte(code))
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(res.Symbols) != 1 {
			t.Fatalf("expected 1 symbol, got %d", len(res.Symbols))
		}
		execSym := res.Symbols[0]
		expectedCalls := []string{"init_db", "run_worker"}
		if !reflect.DeepEqual(execSym.Calls, expectedCalls) {
			t.Errorf("expected execute calls %v, got %v", expectedCalls, execSym.Calls)
		}
	})
}

// TestParser_CompositeLit_TypeUsage validates SPEC-004 RF-1:
// Extraction of type references in CompositeLit (scoredItem{}), ValueSpec (var x scoredItem),
// TypeSpec (struct/interface fields) and function signatures (params and results).
func TestParser_CompositeLit_TypeUsage(t *testing.T) {
	dispatcher := NewDispatcher()

	code := `package main

type scoredItem struct {
	id    int
	score float64
}

type TokenValidator interface {
	Validate() bool
}

type WorkerConfig struct {
	item      scoredItem
	validator TokenValidator
}

func ProcessItem(item scoredItem) scoredItem {
	var local scoredItem
	slice := []scoredItem{scoredItem{id: 1, score: 2.5}}
	_ = local
	_ = slice
	return item
}
`
	res, err := dispatcher.Parse("pkg/service/item.go", []byte(code))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(res.Symbols) != 4 {
		t.Fatalf("expected 4 symbols, got %d", len(res.Symbols))
	}

	var workerConfigSym *Symbol
	var processItemSym *Symbol
	for i := range res.Symbols {
		if res.Symbols[i].Name == "WorkerConfig" {
			workerConfigSym = &res.Symbols[i]
		}
		if res.Symbols[i].Name == "ProcessItem" {
			processItemSym = &res.Symbols[i]
		}
	}

	if workerConfigSym == nil {
		t.Fatal("WorkerConfig symbol not found")
	}
	assertContainsCall(t, workerConfigSym.Calls, "scoredItem")
	assertContainsCall(t, workerConfigSym.Calls, "TokenValidator")

	if processItemSym == nil {
		t.Fatal("ProcessItem symbol not found")
	}
	assertContainsCall(t, processItemSym.Calls, "scoredItem")
}

func assertContainsCall(t *testing.T, calls []string, expected string) {
	t.Helper()
	for _, c := range calls {
		if c == expected {
			return
		}
	}
	t.Errorf("expected calls %v to contain %q", calls, expected)
}
