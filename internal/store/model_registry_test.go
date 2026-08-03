package store

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
)

const lesserHostModulePath = "github.com/equaltoai/lesser-host"

var tableNameRegistryExclusions = map[string]string{
	// hostedGenesisFailureShapeProbe is an any-typed compatibility reader for
	// one legacy attribute shape, not a durable model written by the control plane.
	lesserHostModulePath + "/internal/store.hostedGenesisFailureShapeProbe": "compatibility-only read probe",
}

func TestRegisteredModelsCoverProductionTableNameTypes(t *testing.T) {
	repositoryRoot := findRepositoryRoot(t)
	productionTypes := findProductionTableNameTypes(t, repositoryRoot)
	registeredTypes := make(map[string]struct{}, len(registeredModels()))
	for _, registered := range registeredModels() {
		modelType := reflect.TypeOf(registered)
		if modelType.Kind() == reflect.Pointer {
			modelType = modelType.Elem()
		}
		registeredTypes[qualifiedTypeName(modelType.PkgPath(), modelType.Name())] = struct{}{}
	}

	var failures []string
	for typeName, reason := range tableNameRegistryExclusions {
		if strings.TrimSpace(reason) == "" {
			failures = append(failures, fmt.Sprintf("registry exclusion %s has no reason", typeName))
		}
		if _, ok := productionTypes[typeName]; !ok {
			failures = append(failures, fmt.Sprintf("registry exclusion %s is stale", typeName))
		}
	}
	for typeName, location := range productionTypes {
		if _, ok := registeredTypes[typeName]; ok {
			continue
		}
		if _, ok := tableNameRegistryExclusions[typeName]; ok {
			continue
		}
		failures = append(failures, fmt.Sprintf(
			"%s at %s implements TableName() but is neither registered nor explicitly excluded",
			typeName,
			location,
		))
	}

	sort.Strings(failures)
	for _, failure := range failures {
		t.Error(failure)
	}
}

func findRepositoryRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("get working directory: %v", err)
	}
	for {
		if _, statErr := os.Stat(filepath.Join(dir, "go.mod")); statErr == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("find repository root: go.mod not found")
		}
		dir = parent
	}
}

func findProductionTableNameTypes(t *testing.T, repositoryRoot string) map[string]string {
	t.Helper()
	types := make(map[string]string)
	internalRoot := filepath.Join(repositoryRoot, "internal")
	err := filepath.WalkDir(internalRoot, func(path string, entry fs.DirEntry, walkErr error) error {
		return recordProductionTableNameTypes(repositoryRoot, path, entry, walkErr, types)
	})
	if err != nil {
		t.Fatalf("sweep production TableName methods: %v", err)
	}
	return types
}

func recordProductionTableNameTypes(repositoryRoot string, path string, entry fs.DirEntry, walkErr error, types map[string]string) error {
	if walkErr != nil {
		return walkErr
	}
	if entry.IsDir() || filepath.Ext(path) != ".go" || strings.HasSuffix(path, "_test.go") {
		return nil
	}

	fileSet := token.NewFileSet()
	parsed, err := parser.ParseFile(fileSet, path, nil, 0)
	if err != nil {
		return fmt.Errorf("parse %s: %w", path, err)
	}
	packagePath, err := productionPackagePath(repositoryRoot, path)
	if err != nil {
		return err
	}
	relativeFile, err := filepath.Rel(repositoryRoot, path)
	if err != nil {
		return fmt.Errorf("resolve source path for %s: %w", path, err)
	}

	for _, declaration := range parsed.Decls {
		method, ok := declaration.(*ast.FuncDecl)
		if !ok || !isTableNameMethod(method) {
			continue
		}
		receiverName, ok := receiverTypeName(method.Recv.List[0].Type)
		if !ok {
			return fmt.Errorf("resolve TableName receiver in %s", path)
		}
		position := fileSet.Position(method.Pos())
		types[qualifiedTypeName(packagePath, receiverName)] = fmt.Sprintf("%s:%d", filepath.ToSlash(relativeFile), position.Line)
	}
	return nil
}

func productionPackagePath(repositoryRoot string, path string) (string, error) {
	relativeDir, err := filepath.Rel(repositoryRoot, filepath.Dir(path))
	if err != nil {
		return "", fmt.Errorf("resolve package path for %s: %w", path, err)
	}
	return lesserHostModulePath + "/" + filepath.ToSlash(relativeDir), nil
}

func isTableNameMethod(method *ast.FuncDecl) bool {
	if method.Recv == nil || len(method.Recv.List) != 1 || method.Name.Name != "TableName" {
		return false
	}
	if method.Type.Params != nil && method.Type.Params.NumFields() != 0 {
		return false
	}
	if method.Type.Results == nil || method.Type.Results.NumFields() != 1 {
		return false
	}
	result, ok := method.Type.Results.List[0].Type.(*ast.Ident)
	return ok && result.Name == "string"
}

func receiverTypeName(expression ast.Expr) (string, bool) {
	switch receiver := expression.(type) {
	case *ast.Ident:
		return receiver.Name, true
	case *ast.StarExpr:
		return receiverTypeName(receiver.X)
	case *ast.IndexExpr:
		return receiverTypeName(receiver.X)
	case *ast.IndexListExpr:
		return receiverTypeName(receiver.X)
	default:
		return "", false
	}
}

func qualifiedTypeName(packagePath string, typeName string) string {
	return packagePath + "." + typeName
}
