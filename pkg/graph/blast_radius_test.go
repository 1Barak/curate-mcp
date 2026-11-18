package graph

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
	"testing"
)

// TestBlastRadius_ExcludesDefinesEdges validates that traversal of upstream callers P(u)
// and downstream callees S(u) strictly traverses functional edges (calls, imports)
// and completely ignores structural defines edges.
func TestBlastRadius_ExcludesDefinesEdges(t *testing.T) {
	engine := NewEngine()

	// File nodes
	_ = engine.AddNode(Node{ID: "pkg/auth/auth.go", Label: "auth.go", FilePath: "pkg/auth/auth.go", Type: NodeFile})
	_ = engine.AddNode(Node{ID: "pkg/handler/login.go", Label: "login.go", FilePath: "pkg/handler/login.go", Type: NodeFile})
	_ = engine.AddNode(Node{ID: "pkg/db/user.go", Label: "user.go", FilePath: "pkg/db/user.go", Type: NodeFile})

	// Symbol nodes
	authSym := "pkg/auth/auth.go:AuthService"
	loginSym := "pkg/handler/login.go:HandleLogin"
	dbSym := "pkg/db/user.go:FindUserByID"

	_ = engine.AddNode(Node{ID: authSym, Label: "AuthService", FilePath: "pkg/auth/auth.go", Type: NodeSymbol})
	_ = engine.AddNode(Node{ID: loginSym, Label: "HandleLogin", FilePath: "pkg/handler/login.go", Type: NodeSymbol})
	_ = engine.AddNode(Node{ID: dbSym, Label: "FindUserByID", FilePath: "pkg/db/user.go", Type: NodeSymbol})

	// Structural defines edges (file -> symbol)
	_ = engine.AddEdge(Edge{From: "pkg/auth/auth.go", To: authSym, Type: EdgeDefines})
	_ = engine.AddEdge(Edge{From: "pkg/handler/login.go", To: loginSym, Type: EdgeDefines})
	_ = engine.AddEdge(Edge{From: "pkg/db/user.go", To: dbSym, Type: EdgeDefines})

	// Functional edges: HandleLogin calls AuthService; AuthService calls FindUserByID
	_ = engine.AddEdge(Edge{From: loginSym, To: authSym, Type: EdgeCalls})
	_ = engine.AddEdge(Edge{From: authSym, To: dbSym, Type: EdgeCalls})

	res, err := engine.CalculateBlastRadius(authSym, 2, 50)
	if err != nil {
		t.Fatalf("CalculateBlastRadius failed: %v", err)
	}
	if res == nil {
		t.Fatal("expected non-nil BlastRadiusResult")
	}

	// 1. InDegree and OutDegree must count only functional edges incident on authSym
	// InDegree = 1 (loginSym), ignoring EdgeDefines from pkg/auth/auth.go
	if res.InDegree != 1 {
		t.Errorf("expected InDegree=1 (excluding defines), got %d", res.InDegree)
	}
	if res.OutDegree != 1 {
		t.Errorf("expected OutDegree=1 (calls dbSym), got %d", res.OutDegree)
	}

	// 2. Upstream callers P(u) must include loginSym and NEVER file nodes
	foundLogin := false
	for _, caller := range res.UpstreamCallers {
		if caller == loginSym {
			foundLogin = true
		}
		if strings.HasSuffix(caller, ".go") && !strings.Contains(caller, ":") {
			t.Errorf("structural violation: file node %q leaked into UpstreamCallers", caller)
		}
	}
	if !foundLogin {
		t.Errorf("expected %q in UpstreamCallers %v", loginSym, res.UpstreamCallers)
	}

	// 3. Downstream callees S(u) must include dbSym
	foundDB := false
	for _, callee := range res.DownstreamCallees {
		if callee == dbSym {
			foundDB = true
		}
	}
	if !foundDB {
		t.Errorf("expected %q in DownstreamCallees %v", dbSym, res.DownstreamCallees)
	}
}

// TestBlastRadius_ExactCriticalityFormulaAndDepthInvariance validates that:
// Ic = (in_degree_func * 3) + out_degree_func
// is calculated strictly on direct 1-hop functional edges incident on u,
// and remains invariant across different traversal depth parameters.
func TestBlastRadius_ExactCriticalityFormulaAndDepthInvariance(t *testing.T) {
	engine := NewEngine()

	target := "target:Symbol"
	_ = engine.AddNode(Node{ID: target, Label: "Target", FilePath: "target.go", Type: NodeSymbol})

	// 2 direct 1-hop callers: in_degree_func = 2
	c1 := "caller:1"
	c2 := "caller:2"
	_ = engine.AddNode(Node{ID: c1, Label: "Caller1", FilePath: "c1.go", Type: NodeSymbol})
	_ = engine.AddNode(Node{ID: c2, Label: "Caller2", FilePath: "c2.go", Type: NodeSymbol})
	_ = engine.AddEdge(Edge{From: c1, To: target, Type: EdgeCalls})
	_ = engine.AddEdge(Edge{From: c2, To: target, Type: EdgeCalls})

	// 1 2-hop caller: c3 -> c1 -> target
	c3 := "caller:3"
	_ = engine.AddNode(Node{ID: c3, Label: "Caller3", FilePath: "c3.go", Type: NodeSymbol})
	_ = engine.AddEdge(Edge{From: c3, To: c1, Type: EdgeCalls})

	// 3 direct 1-hop callees: out_degree_func = 3
	s1 := "callee:1"
	s2 := "callee:2"
	s3 := "callee:3"
	_ = engine.AddNode(Node{ID: s1, Label: "Callee1", FilePath: "s1.go", Type: NodeSymbol})
	_ = engine.AddNode(Node{ID: s2, Label: "Callee2", FilePath: "s2.go", Type: NodeSymbol})
	_ = engine.AddNode(Node{ID: s3, Label: "Callee3", FilePath: "s3.go", Type: NodeSymbol})
	_ = engine.AddEdge(Edge{From: target, To: s1, Type: EdgeCalls})
	_ = engine.AddEdge(Edge{From: target, To: s2, Type: EdgeImports})
	_ = engine.AddEdge(Edge{From: target, To: s3, Type: EdgeCalls})

	// 2 2-hop callees: target -> s1 -> sSub1, target -> s2 -> sSub2
	sSub1 := "callee:sub1"
	sSub2 := "callee:sub2"
	_ = engine.AddNode(Node{ID: sSub1, Label: "Sub1", FilePath: "sub1.go", Type: NodeSymbol})
	_ = engine.AddNode(Node{ID: sSub2, Label: "Sub2", FilePath: "sub2.go", Type: NodeSymbol})
	_ = engine.AddEdge(Edge{From: s1, To: sSub1, Type: EdgeCalls})
	_ = engine.AddEdge(Edge{From: s2, To: sSub2, Type: EdgeCalls})

	// Expected formula:
	// in_degree_func = 2
	// out_degree_func = 3
	// Ic = (2 * 3) + 3 = 9
	const expectedIn = 2
	const expectedOut = 3
	const expectedIc = 9

	depthsToTest := []int{1, 2, 3, 5}
	for _, d := range depthsToTest {
		t.Run(fmt.Sprintf("depth_%d", d), func(t *testing.T) {
			res, err := engine.CalculateBlastRadius(target, d, 50)
			if err != nil {
				t.Fatalf("CalculateBlastRadius failed at depth=%d: %v", d, err)
			}
			if res == nil {
				t.Fatal("expected non-nil result")
			}

			if res.InDegree != expectedIn {
				t.Errorf("depth=%d: expected InDegree=%d, got %d", d, expectedIn, res.InDegree)
			}
			if res.OutDegree != expectedOut {
				t.Errorf("depth=%d: expected OutDegree=%d, got %d", d, expectedOut, res.OutDegree)
			}
			if res.Criticality != expectedIc {
				t.Errorf("depth=%d: expected Criticality=%d, got %d", d, expectedIc, res.Criticality)
			}
		})
	}
}

// TestBlastRadius_DefaultsAndBounds validates default handling and bound clamping:
// depth <= 0 defaults to 2; depth > 5 clamped to 5.
// max_nodes <= 0 defaults to 50; max_nodes > 200 clamped to 200.
func TestBlastRadius_DefaultsAndBounds(t *testing.T) {
	engine := NewEngine()

	// Build a linear chain of 10 nodes: N0 -> N1 -> ... -> N9
	const totalNodes = 10
	for i := 0; i < totalNodes; i++ {
		_ = engine.AddNode(Node{
			ID:       fmt.Sprintf("node:%d", i),
			Label:    fmt.Sprintf("Node%d", i),
			FilePath: "chain.go",
			Type:     NodeSymbol,
		})
		if i > 0 {
			_ = engine.AddEdge(Edge{
				From: fmt.Sprintf("node:%d", i-1),
				To:   fmt.Sprintf("node:%d", i),
				Type: EdgeCalls,
			})
		}
	}

	// 1. depth=0 must default to depth=2 (N0 should see N1 and N2 downstream)
	resDefault, err := engine.CalculateBlastRadius("node:0", 0, 50)
	if err != nil {
		t.Fatalf("failed with depth=0: %v", err)
	}
	if len(resDefault.DownstreamCallees) != 2 {
		t.Errorf("depth=0 default to 2: expected 2 downstream callees, got %d (%v)",
			len(resDefault.DownstreamCallees), resDefault.DownstreamCallees)
	}

	// 2. depth=10 must be bounded to depth=5 (N0 should see at most N1..N5, 5 callees)
	resBoundedDepth, err := engine.CalculateBlastRadius("node:0", 10, 50)
	if err != nil {
		t.Fatalf("failed with depth=10: %v", err)
	}
	if len(resBoundedDepth.DownstreamCallees) != 5 {
		t.Errorf("depth=10 bounded to 5: expected 5 downstream callees, got %d (%v)",
			len(resBoundedDepth.DownstreamCallees), resBoundedDepth.DownstreamCallees)
	}

	// 3. max_nodes bound test: create a star topology with 250 outgoing callees
	starEngine := NewEngine()
	root := "star:root"
	_ = starEngine.AddNode(Node{ID: root, Label: "Root", FilePath: "star.go", Type: NodeSymbol})
	for i := 0; i < 250; i++ {
		leaf := fmt.Sprintf("leaf:%d", i)
		_ = starEngine.AddNode(Node{ID: leaf, Label: leaf, FilePath: "star.go", Type: NodeSymbol})
		_ = starEngine.AddEdge(Edge{From: root, To: leaf, Type: EdgeCalls})
	}

	// max_nodes=500 must be bounded to 200
	resBoundedNodes, err := starEngine.CalculateBlastRadius(root, 1, 500)
	if err != nil {
		t.Fatalf("failed with max_nodes=500: %v", err)
	}
	if len(resBoundedNodes.DownstreamCallees) > 200 {
		t.Errorf("max_nodes=500 bounded to 200: expected <= 200 callees, got %d",
			len(resBoundedNodes.DownstreamCallees))
	}

	// max_nodes=0 must default to 50
	resDefaultNodes, err := starEngine.CalculateBlastRadius(root, 1, 0)
	if err != nil {
		t.Fatalf("failed with max_nodes=0: %v", err)
	}
	if len(resDefaultNodes.DownstreamCallees) != 50 {
		t.Errorf("max_nodes=0 default to 50: expected exactly 50 callees, got %d",
			len(resDefaultNodes.DownstreamCallees))
	}
}

// TestBlastRadius_RiskTiers validates the exact deterministic mapping to the 4 risk tiers:
// BAJO (< 3), MEDIO (3 <= Ic < 6), ALTO (6 <= Ic < 12), CRÍTICO (>= 12).
func TestBlastRadius_RiskTiers(t *testing.T) {
	tests := []struct {
		name         string
		inCalls      int
		outCalls     int
		expectedIc   int
		expectedRisk RiskLevel
		expectedFTD  bool
	}{
		{
			name:         "BAJO risk tier (Ic=2 < 3)",
			inCalls:      0,
			outCalls:     2,
			expectedIc:   2,
			expectedRisk: RiskBajo,
			expectedFTD:  true,
		},
		{
			name:         "MEDIO risk tier (Ic=3)",
			inCalls:      1,
			outCalls:     0,
			expectedIc:   3,
			expectedRisk: RiskMedio,
			expectedFTD:  false,
		},
		{
			name:         "MEDIO risk tier (Ic=5)",
			inCalls:      1,
			outCalls:     2,
			expectedIc:   5,
			expectedRisk: RiskMedio,
			expectedFTD:  false,
		},
		{
			name:         "ALTO risk tier (Ic=6)",
			inCalls:      2,
			outCalls:     0,
			expectedIc:   6,
			expectedRisk: RiskAlto,
			expectedFTD:  false,
		},
		{
			name:         "ALTO risk tier (Ic=11)",
			inCalls:      3,
			outCalls:     2,
			expectedIc:   11,
			expectedRisk: RiskAlto,
			expectedFTD:  false,
		},
		{
			name:         "CRÍTICO risk tier (Ic=12)",
			inCalls:      4,
			outCalls:     0,
			expectedIc:   12,
			expectedRisk: RiskCritico,
			expectedFTD:  false,
		},
		{
			name:         "CRÍTICO risk tier (Ic=16)",
			inCalls:      5,
			outCalls:     1,
			expectedIc:   16,
			expectedRisk: RiskCritico,
			expectedFTD:  false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			eng := NewEngine()
			u := "u:target"
			_ = eng.AddNode(Node{ID: u, Label: "Target", FilePath: "u.go", Type: NodeSymbol})

			for i := 0; i < tc.inCalls; i++ {
				c := fmt.Sprintf("c:%d", i)
				_ = eng.AddNode(Node{ID: c, Label: c, FilePath: "c.go", Type: NodeSymbol})
				_ = eng.AddEdge(Edge{From: c, To: u, Type: EdgeCalls})
			}
			for i := 0; i < tc.outCalls; i++ {
				s := fmt.Sprintf("s:%d", i)
				_ = eng.AddNode(Node{ID: s, Label: s, FilePath: "s.go", Type: NodeSymbol})
				_ = eng.AddEdge(Edge{From: u, To: s, Type: EdgeCalls})
			}

			res, err := eng.CalculateBlastRadius(u, 2, 50)
			if err != nil {
				t.Fatalf("CalculateBlastRadius failed: %v", err)
			}
			if res == nil {
				t.Fatal("expected non-nil result")
			}

			if res.Criticality != tc.expectedIc {
				t.Errorf("criticality mismatch: expected %d, got %d", tc.expectedIc, res.Criticality)
			}
			if res.Risk != tc.expectedRisk {
				t.Errorf("risk tier mismatch: expected %s, got %s", tc.expectedRisk, res.Risk)
			}
			if res.FTDEligible != tc.expectedFTD {
				t.Errorf("FTDEligible mismatch: expected %v, got %v", tc.expectedFTD, res.FTDEligible)
			}
		})
	}
}

// TestBlastRadius_InvalidOrNonExistentNode validates that missing, empty,
// or non-existent node parameters trigger specific domain errors.
func TestBlastRadius_InvalidOrNonExistentNode(t *testing.T) {
	engine := NewEngine()
	_ = engine.AddNode(Node{ID: "valid:node", Label: "Valid", FilePath: "v.go", Type: NodeSymbol})

	// 1. Empty string node must return ErrInvalidParams
	_, errEmpty := engine.CalculateBlastRadius("", 2, 50)
	if errEmpty == nil {
		t.Error("expected error for empty node name, got nil")
	} else if !errors.Is(errEmpty, ErrInvalidParams) {
		t.Errorf("expected ErrInvalidParams, got: %v", errEmpty)
	}

	// 2. Non-existent node must return ErrNodeNotFound
	_, errNotFound := engine.CalculateBlastRadius("ghost:symbol", 2, 50)
	if errNotFound == nil {
		t.Error("expected error for non-existent node name, got nil")
	} else if !errors.Is(errNotFound, ErrNodeNotFound) {
		t.Errorf("expected ErrNodeNotFound, got: %v", errNotFound)
	}
}

// TestBlastRadius_OTelTraceFormat validates the exact canonical format of the OTel trace string:
// [FTD_METRICS: trace_id=<uuid>, in_degree=<in>, out_degree=<out>, ic=<ic>, ftd_eligible=<bool>]
func TestBlastRadius_OTelTraceFormat(t *testing.T) {
	engine := NewEngine()
	node := "sym:otel"
	_ = engine.AddNode(Node{ID: node, Label: "OtelSymbol", FilePath: "otel.go", Type: NodeSymbol})

	// 1 incoming caller (in=1) and 2 outgoing callees (out=2) -> ic = (1*3) + 2 = 5 -> ftd_eligible=false
	_ = engine.AddNode(Node{ID: "c1", Label: "c1", FilePath: "c.go", Type: NodeSymbol})
	_ = engine.AddEdge(Edge{From: "c1", To: node, Type: EdgeCalls})
	_ = engine.AddNode(Node{ID: "s1", Label: "s1", FilePath: "s.go", Type: NodeSymbol})
	_ = engine.AddNode(Node{ID: "s2", Label: "s2", FilePath: "s.go", Type: NodeSymbol})
	_ = engine.AddEdge(Edge{From: node, To: "s1", Type: EdgeCalls})
	_ = engine.AddEdge(Edge{From: node, To: "s2", Type: EdgeImports})

	res, err := engine.CalculateBlastRadius(node, 2, 50)
	if err != nil {
		t.Fatalf("CalculateBlastRadius failed: %v", err)
	}
	if res == nil {
		t.Fatal("expected non-nil result")
	}

	if res.TraceID == "" {
		t.Error("expected non-empty trace_id")
	}

	// Canonical regex pattern:
	// ^\[FTD_METRICS: trace_id=[0-9a-fA-F\-]{32,36}, in_degree=\d+, out_degree=\d+, ic=\d+, ftd_eligible=(true|false)\]$
	otelRegex := regexp.MustCompile(`^\[FTD_METRICS: trace_id=[0-9a-fA-F\-]{32,36}, in_degree=1, out_degree=2, ic=5, ftd_eligible=false\]$`)
	if !otelRegex.MatchString(res.OTelTrace) {
		t.Errorf("OTel trace %q does not match expected canonical format", res.OTelTrace)
	}
}
