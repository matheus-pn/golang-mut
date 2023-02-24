package main

import (
	"bytes"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"os/exec"
	"strings"
	"time"
)

type PackageInfo struct {
	Dir          string
	ImportPath   string
	GoFiles      []string
	TestGoFiles  []string
	ReachDefined bool
}

type Mutator interface {
	replacement(ast.Node) ast.Node
	match(ast.Node) bool
}

type ChangeMode int

const (
	SC_APPEND ChangeMode = iota
	SC_REPLACE
	SC_DELETE
)

type FileInfo struct {
	Id      int
	Path    string
	Source  []byte
	Package *PackageInfo
	Changes map[token.Pos]*SourceChange
	AST     *ast.File
	Imports map[string]bool
}

// Even parsing with comments still can mess compiler directives
// This is a way to change the ast without moving anything else
type SourceChange struct {
	Mode ChangeMode
	Code string
	End  token.Pos
}

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

var (
	DEFAULT_MUTATORS = []Mutator{Incrementer{}}
	TMP_ROOT         string
	FLAGS            = map[string]string{
		"verbose": "false",
	}
)

// FIXME: Use only packages.Load instead of go list
func packageInfo() []PackageInfo {
	// Output will be a series of lines
	// each line will have a semicolon separated list of:
	// Dir ; ImportPath ; GoFiles ; TestGofiles
	// GoFiles and TestGofiles are Comma Separated Values
	Verbosef("EXEC go list -f {{.Dir}};{{.ImportPath}};{{range .GoFiles}}{{.}},{{end}};{{range .TestGoFiles}}{{.}},{{end}} ./...\n")
	out, err := exec.Command(
		"go", "list", "-f",
		"{{.Dir}};{{.ImportPath}};{{range .GoFiles}}{{.}},{{end}};{{range .TestGoFiles}}{{.}},{{end}}",
		"./...",
	).Output()
	if err != nil {
		panic(err)
	}
	lines := strings.Split(string(out), "\n")
	// Ignore empty last line
	lines = lines[:len(lines)-1]

	infoList := []PackageInfo{}
	for _, line := range lines {
		fields := strings.Split(line, ";")
		goFiles := strings.Split(fields[2], ",")
		gotestFiles := strings.Split(fields[3], ",")

		pack := PackageInfo{
			fields[0],
			fields[1],
			// Ignore trailing comma
			goFiles[:len(goFiles)-1],
			gotestFiles[:len(gotestFiles)-1],
			false,
		}
		infoList = append(infoList, pack)
	}
	return infoList
}

func checkGolist() {
	Verbosef("EXEC go list .\n")
	err := exec.Command("go", "list", ".").Run()
	if err != nil {
		fmt.Println(err.Error())
		os.Exit(1)
	}
}

// Prints to stdout only if the global verbose flag is set
func Verbosef(msg string, args ...interface{}) {
	if FLAGS["verbose"] != "true" {
		return
	}

	fmt.Printf(msg, args...)
}

// Changes the current working directory to directory
// Executes the callback function
// Changes back to the previous working directory
func atDir(directory string, callback func()) {
	current, _ := exec.Command("pwd").Output()

	Verbosef("CD %s\n", directory)
	os.Chdir(directory)
	callback()
	Verbosef("CD %s\n", string(current))
	os.Chdir(string(current))
}

// Copies directory to a temporary folder in /tmp/MUT-xxxxxx
func copyProject(directory string) string {
	tmpFilepath := fmt.Sprintf("%s/MUT-%06d", TMP_DIR, time.Now().Nanosecond())
	Verbosef("COPY %s to %s\n", directory, tmpFilepath)
	err := exec.Command("cp", "-r", directory, tmpFilepath).Run()
	if err != nil {
		panic(err)
	}
	return tmpFilepath
}

func removeProjectCopy(directory string) {
	Verbosef("REMOVE %s\n", directory)
	err := exec.Command("rm", "-r", "-f", directory).Run()
	if err != nil {
		panic(err)
	}
}

var ONE = ast.BasicLit{ValuePos: token.NoPos, Kind: token.INT, Value: "1"}

type Incrementer struct{}

func (Incrementer) replacement(orig ast.Node) ast.Node {
	node, ok := orig.(*ast.BasicLit)
	if !ok {
		return nil
	}
	if node.Kind != token.FLOAT && node.Kind != token.INT {
		return nil
	}

	return &ast.BinaryExpr{X: node, OpPos: token.NoPos, Op: token.ADD, Y: &ONE}
}

func (i Incrementer) match(orig ast.Node) bool {
	return i.replacement(orig) != nil
}

// func schemata[T any](toggle string, origEx T, replaceEx ...T) T {
// 	return origEx
// }

func hasMutation(node ast.Node, mutators []Mutator) bool {
	for _, mut := range mutators {
		if mut.match(node) {
			return true
		}
	}
	return false
}

func isSpecialBlock(node ast.Node) bool {
	switch node.(type) {
	case *ast.SelectStmt, *ast.SwitchStmt, *ast.TypeSwitchStmt:
		return true
	}
	return false
}

func (file *FileInfo) addSourceChange(sc *SourceChange, at token.Pos) {
	file.Changes[at] = sc
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

func (file *FileInfo) addInstrumentationGo() {
	visited := make(map[ast.Node]bool)
	path := []ast.Node{}
	astWalk := func(node ast.Node) bool {
		if node == nil {
			path = path[:len(path)-1]
			return false
		}
		path = append(path, node)
		// if this node has mutations we will instrument the enclosing block to check reachability at runtime
		if match := hasMutation(node, DEFAULT_MUTATORS); !match {
			return true
		}

		parentNode, at := getParent(path)
		if parentNode == nil {
			return true
		}

		// block not already visited
		if !visited[parentNode] {
			visited[parentNode] = true
			// Create __reach("BLOCK_ID:FILEPATH") call
			reach := fmt.Sprintf(`__reach("R %d:%d", false);`, file.Id, parentNode.Pos())

			file.addSourceChange(&SourceChange{SC_APPEND, reach, parentNode.End()}, at)
		}
		return false
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

func (file *FileInfo) writeChanges() {
	if len(file.Changes) == 0 {
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
			}
		}
		writer.WriteByte(file.Source[i])
	}
	if !file.Package.ReachDefined {
		fmt.Fprintf(&writer, REACH_DEFINITION, TMP_ROOT)
		file.Package.ReachDefined = true
	}
	os.WriteFile(file.Path, writer.Bytes(), 0777)
}

// Adds the __reach function on the file at goFilePath
func (i *Instrumenter) NewFileInfo(path string, pack *PackageInfo) *FileInfo {
	var fs token.FileSet
	var err error

	file := FileInfo{
		Path: path, Package: pack, Changes: make(map[token.Pos]*SourceChange),
		Imports: make(map[string]bool),
	}

	file.Source, err = os.ReadFile(path)
	if err != nil {
		panic(err)
	}

	file.AST, err = parser.ParseFile(&fs, path, file.Source, 0)
	if err != nil {
		panic(err)
	}

	for _, spec := range file.AST.Imports {
		file.Imports[spec.Path.Value] = true
	}

	file.Id = len(i.Files)
	i.Files = append(i.Files, &file)
	return &file
}

func (i *Instrumenter) instrumentPackage(p *PackageInfo) bool {
	// FIXME: Check before copying project
	if len(p.TestGoFiles) == 0 {
		fmt.Printf("?\t%s\t[no test files]\n", p.ImportPath)
		// return false
	} else if len(p.GoFiles) == 0 {
		fmt.Printf("?\t%s\t[no .go files]\n", p.ImportPath)
		return false
	}

	for _, source := range p.GoFiles {
		file := i.NewFileInfo(p.Dir+"/"+source, p)
		file.addInstrumentationGo()
		file.writeChanges()
	}
	for _, source := range p.TestGoFiles {
		file := i.NewFileInfo(p.Dir+"/"+source, p)
		file.addInstrumentationTEST()
		file.writeChanges()
	}
	return true
}

type Instrumenter struct {
	Files []*FileInfo
}

func mutateAndRun() {
	i := Instrumenter{}
	for _, p := range packageInfo()[1:] {
		shouldCompute := i.instrumentPackage(&p)
		if !shouldCompute {
			return
		}

		// fmt.Println("computing mutation reach >> go test " + p.ImportPath)
		// err := exec.Command("go", "test", p.ImportPath).Run()
		// if err != nil {
		// 	panic(err)
		// }
		// data, err := os.ReadFile("reach.log")
		// if err != nil {
		// 	panic(err)
		// }
		// strData := string(data)
		// fmt.Print(strData)
	}
}

func main() {
	var directory string
	if len(os.Args) != 2 {
		directory = "."
	} else {
		directory = os.Args[1]
	}

	// root directory of the project
	atDir(directory, checkGolist)
	path := copyProject(directory)
	TMP_ROOT = path
	// defer removeProjectCopy(path)
	atDir(path, mutateAndRun)
	fmt.Println(path)

}
