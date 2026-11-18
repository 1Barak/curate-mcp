package parser

import (
	"testing"
)

// TestDetectLanguage_SevenCoreLanguages verifies language detection from extensions.
func TestDetectLanguage_SevenCoreLanguages(t *testing.T) {
	dispatcher := NewDispatcher()

	tests := []struct {
		filePath string
		expected string
		wantErr  bool
	}{
		// 1. Go
		{"cmd/main.go", "go", false},
		{"pkg/graph/engine.go", "go", false},
		// 2. Python
		{"scripts/build.py", "python", false},
		{"server/app.py", "python", false},
		// 3. TypeScript
		{"frontend/src/index.ts", "typescript", false},
		{"frontend/src/App.tsx", "typescript", false},
		// 4. JavaScript
		{"web/server.js", "javascript", false},
		{"web/component.jsx", "javascript", false},
		{"web/module.mjs", "javascript", false},
		{"web/config.cjs", "javascript", false},
		// 5. Rust
		{"src/lib.rs", "rust", false},
		{"src/main.rs", "rust", false},
		// 6. C / C++
		{"native/buffer.c", "c", false},
		{"native/buffer.h", "c", false},
		{"native/core.cpp", "cpp", false},
		{"native/core.hpp", "cpp", false},
		{"native/engine.cc", "cpp", false},
		// 7. Java
		{"src/main/java/App.java", "java", false},
		// Unsupported
		{"styles/main.css", "", true},
		{"docs/README.md", "", true},
	}

	for _, tc := range tests {
		t.Run(tc.filePath, func(t *testing.T) {
			lang, err := dispatcher.DetectLanguage(tc.filePath)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("expected error for %s, got nil", tc.filePath)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error for %s: %v", tc.filePath, err)
			}
			if lang != tc.expected {
				t.Errorf("expected language %q, got %q", tc.expected, lang)
			}
		})
	}
}

// TestParse_SevenLanguagesFixtures verifies syntax tree parsing and extraction for all 7 languages.
func TestParse_SevenLanguagesFixtures(t *testing.T) {
	dispatcher := NewDispatcher()

	type expectedSymbol struct {
		name string
		kind SymbolKind
	}

	tests := []struct {
		name            string
		filePath        string
		content         string
		expectedImports []string
		expectedSymbols []expectedSymbol
	}{
		{
			name:     "1. Go language fixture",
			filePath: "pkg/service/user.go",
			content: `package service

import (
	"fmt"
	"net/http"
)

type UserInterface interface {
	GetUser(id string) error
}

type UserService struct {
	client *http.Client
}

func (s *UserService) GetUser(id string) error {
	fmt.Println(id)
	return nil
}

func NewUserService() *UserService {
	return &UserService{}
}
`,
			expectedImports: []string{"fmt", "net/http"},
			expectedSymbols: []expectedSymbol{
				{"UserInterface", KindInterface},
				{"UserService", KindStruct},
				{"GetUser", KindMethod},
				{"NewUserService", KindFunction},
			},
		},
		{
			name:     "2. Python language fixture",
			filePath: "app/data/processor.py",
			content: `import os
from datetime import datetime

class DataPipeline:
    def execute(self, payload):
        return True

def clean_records(records):
    pass
`,
			expectedImports: []string{"os", "datetime"},
			expectedSymbols: []expectedSymbol{
				{"DataPipeline", KindClass},
				{"execute", KindMethod},
				{"clean_records", KindFunction},
			},
		},
		{
			name:     "3. TypeScript / TSX language fixture",
			filePath: "src/auth/token.ts",
			content: `import { Router } from "express";

export interface SessionToken {
    jwt: string;
}

export class TokenVerifier {
    public verify(token: string): boolean {
        return true;
    }
}

export function generateToken(): string {
    return "secret";
}
`,
			expectedImports: []string{"express"},
			expectedSymbols: []expectedSymbol{
				{"SessionToken", KindInterface},
				{"TokenVerifier", KindClass},
				{"verify", KindMethod},
				{"generateToken", KindFunction},
			},
		},
		{
			name:     "4. JavaScript / JSX language fixture",
			filePath: "src/api/client.js",
			content: `const axios = require('axios');

class APIClient {
    fetchData(endpoint) {
        return axios.get(endpoint);
    }
}

function createClient() {
    return new APIClient();
}
`,
			expectedImports: []string{"axios"},
			expectedSymbols: []expectedSymbol{
				{"APIClient", KindClass},
				{"fetchData", KindMethod},
				{"createClient", KindFunction},
			},
		},
		{
			name:     "5. Rust language fixture",
			filePath: "src/storage/engine.rs",
			content: `use std::sync::Arc;

pub trait StorageEngine {
    fn read(&self, key: &str) -> Option<Vec<u8>>;
}

pub struct MemoryEngine {
    data: Arc<Vec<u8>>,
}

impl MemoryEngine {
    pub fn new() -> Self {
        MemoryEngine { data: Arc::new(vec![]) }
    }
}

pub fn initialize_engine() -> MemoryEngine {
    MemoryEngine::new()
}
`,
			expectedImports: []string{"std::sync::Arc"},
			expectedSymbols: []expectedSymbol{
				{"StorageEngine", KindInterface},
				{"MemoryEngine", KindStruct},
				{"new", KindMethod},
				{"initialize_engine", KindFunction},
			},
		},
		{
			name:     "6. C / C++ language fixture",
			filePath: "native/memory_pool.cpp",
			content: `#include <vector>
#include <string>

class MemoryPool {
public:
    void* allocate(size_t size) {
        return nullptr;
    }
};

void reset_pool() {
}
`,
			expectedImports: []string{"vector", "string"},
			expectedSymbols: []expectedSymbol{
				{"MemoryPool", KindClass},
				{"allocate", KindMethod},
				{"reset_pool", KindFunction},
			},
		},
		{
			name:     "7. Java language fixture",
			filePath: "src/main/java/com/service/PaymentService.java",
			content: `package com.service;

import java.util.List;
import java.util.Map;

public interface PaymentGateway {
    boolean processPayment(double amount);
}

public class PaymentService implements PaymentGateway {
    public boolean processPayment(double amount) {
        return true;
    }

    public static void auditLog() {
    }
}
`,
			expectedImports: []string{"java.util.List", "java.util.Map"},
			expectedSymbols: []expectedSymbol{
				{"PaymentGateway", KindInterface},
				{"PaymentService", KindClass},
				{"processPayment", KindMethod},
				{"auditLog", KindMethod},
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			res, err := dispatcher.Parse(tc.filePath, []byte(tc.content))
			if err != nil {
				t.Fatalf("parser returned unexpected error for %s: %v", tc.filePath, err)
			}
			if res == nil {
				t.Fatalf("parser returned nil result for %s", tc.filePath)
			}

			// Validate extracted imports
			for _, expImp := range tc.expectedImports {
				found := false
				for _, imp := range res.Imports {
					if imp == expImp {
						found = true
						break
					}
				}
				if !found {
					t.Errorf("missing expected import %q in %v", expImp, res.Imports)
				}
			}

			// Validate extracted symbols
			for _, expSym := range tc.expectedSymbols {
				found := false
				for _, sym := range res.Symbols {
					if sym.Name == expSym.name && sym.Kind == expSym.kind {
						found = true
						break
					}
				}
				if !found {
					t.Errorf("missing expected symbol %s (%s) in %v", expSym.name, expSym.kind, res.Symbols)
				}
			}
		})
	}
}
