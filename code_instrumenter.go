// The goal of this step is to instrument the code
package main

import (
	"bytes"
	"fmt"
	"go/ast"
	"go/token"
	"os"
	"os/exec"
)

const (
	TMP_DIR          = "/tmp"
	REACH_DEFINITION = `
var __LOGFILE, _ = os.OpenFile("%s/reach.log", os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0777)
var __SET = make(map[string]bool)

func __reach(msg string, flush bool) {
	if !flush && __SET[msg] {
		return
	} else if !flush {
		__SET[msg] = true
	} else {
		__SET = make(map[string]bool)
	}
	__LOGFILE.WriteString(msg+"\n")
}
`
)

type FileTable struct {
	Files []*FileInfo
}

func (ft *FileTable) InstrumentPackage(pkg *PackageInfo) {
	// If the package has no tests, or is empty: skip
	if len(pkg.TestGoFiles) == 0 {
		fmt.Printf("?\t%s\t[no test files]\n", pkg.ImportPath)
		return
	} else if len(pkg.GoFiles) == 0 {
		fmt.Printf("?\t%s\t[no .go files]\n", pkg.ImportPath)
		return
	}

	// For each Source file
	for _, source := range pkg.GoFiles {
		file := ft.NewFileInfo(pkg.Dir+"/"+source, pkg)
		// Add instrumentation code to compute coverage
		file.addInstrumentationGo()
		file.writeInstrumentation()
	}
	for _, source := range pkg.TestGoFiles {
		file := ft.NewFileInfo(pkg.Dir+"/"+source, pkg)
		// Add instrumentation code to compute coverage
		file.addInstrumentationTEST()
		file.writeInstrumentation()
	}

	// Run tests of package to get coverage data
	Verbosef("computing coverage >> go test " + fmt.Sprintf("%s/%s", TMP_ROOT, pkg.ImportPath))
	os.Chdir(TMP_ROOT)
	out, err := exec.Command("pwd").Output()
	if err == nil {
		Verbosef("PWD " + string(out))
	}
	_, err = exec.Command("go", "test", pkg.ImportPath).Output()
	if err != nil {
		if exit, ok := err.(*exec.ExitError); ok {
			fmt.Println(string(exit.Stderr))
		}
		panic(err)
	}
}

func (file *FileInfo) addInstrumentationGo() {
	visited := make(map[ast.Node]bool)
	path := []ast.Node{}
	astWalk := func(node ast.Node) (ret bool) {
		ret = true

		if node == nil {
			path = path[:len(path)-1]
			return
		}
		path = append(path, node)

		// If this node has mutations we will instrument the enclosing block to check reachability at runtime
		changes := mutations(string(file.Source), node, path, DEFAULT_MUTATORS)
		// if len(changes) == 0 {
		// 	return
		// }

		parentNode, at := getParent(path)
		if parentNode == nil {
			return
		}

		// Here we append mutations to the parent block scope
		muts := []*Mutation{}
		for _, change := range changes {
			// Add the mutation with the actual location
			muts = append(muts, &Mutation{file, change, parentNode.Pos(), false})
		}

		// The mutation being scoped by parent block makes it easier to retrieve info later
		m := file.Mutations[parentNode.Pos()]
		file.Mutations[parentNode.Pos()] = append(m, muts...)

		// Here we add the coverage code (only needs to be done once per block)
		if !visited[parentNode] {
			visited[parentNode] = true
			// Create __reach("BLOCK_ID:FILEPATH") call
			reach := fmt.Sprintf(`__reach("R %d:%d", false);`, file.Id, parentNode.Pos())

			file.addSourceChange(&SourceChange{SC_APPEND, reach, parentNode.End()}, at)
		}
		return
	}
	ast.Inspect(file.AST, astWalk)
}

func (file *FileInfo) addInstrumentationTEST() {
	astWalk := func(n ast.Node) bool {
		if _, ok := n.(*ast.File); ok {
			return true
		}
		if fun, ok := n.(*ast.FuncDecl); !ok || len(fun.Type.Params.List) < 1 {
			return false
		}
		fun := n.(*ast.FuncDecl)
		fields := fun.Type.Params.List
		hasTest := false
		// FIXME: REDUNDANT FOR
		if len(fields) > 1 {
			return false
		}
		for _, f := range fields {
			e, ok := f.Type.(*ast.StarExpr)
			if !ok {
				continue
			}
			s, ok := e.X.(*ast.SelectorExpr)
			if !ok {
				continue
			}
			i, ok := s.X.(*ast.Ident)
			if !ok {
				continue
			}
			if i.Name == "testing" && s.Sel.Name == "T" {
				hasTest = true
				break
			}
		}
		if hasTest {
			reach := fmt.Sprintf(`__reach("T %d:%d", true)`, file.Id, fun.Pos())
			// reach, _ := parser.ParseExpr(reachSrc)
			// fun.Body.List = append([]ast.Stmt{&ast.ExprStmt{X: reach}}, fun.Body.List...)
			file.addSourceChange(&SourceChange{SC_APPEND, reach, fun.Body.Rbrace + 1}, fun.Body.Lbrace+1)
		}
		return false
	}
	ast.Inspect(file.AST, astWalk)
}

func (file *FileInfo) writeInstrumentation() {
	if len(file.Changes) == 0 {
		os.WriteFile(file.Path, []byte(file.Source), 0777)
		return
	}

	writer := bytes.Buffer{}
	for i := 0; i < len(file.Source); i++ {
		if i == int(file.AST.Name.End()) && !file.Imports[`"os"`] && !file.Package.ReachDefined {
			fmt.Fprint(&writer, "\n"+`import "os"`)
		}

		if change, present := file.Changes[token.Pos(i)]; present {
			switch change.Mode {
			case SC_APPEND:
				fmt.Fprint(&writer, change.Code)
			default:
				panic("Invalid program state")
			}
		}
		writer.WriteByte(file.Source[i])
	}

	if !file.Package.ReachDefined {
		fmt.Fprintf(&writer, REACH_DEFINITION, TMP_ROOT)
		file.Package.ReachDefined = true
	}

	os.WriteFile(file.Path, writer.Bytes(), 0777)
	file.Changes = nil
}

func getParent(path []ast.Node) (ast.Node, token.Pos) {
	for i := len(path) - 1; i >= 0; i-- {
		node := path[i]
		switch stmt := node.(type) {
		case *ast.BlockStmt:
			// Block is  a part of switch-like structure, add before instead
			if i > 0 && isSpecialBlock(path[i-1]) {
				continue
			}
			return stmt, stmt.Lbrace + 1
		case *ast.CommClause:
			return stmt, stmt.Colon + 1
		case *ast.CaseClause:
			return stmt, stmt.Colon + 1
		}
	}
	return nil, 0
}
