package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"go/ast"
	"go/parser"
	"go/printer"
	"go/token"
	"math/rand"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
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

type Replacement struct {
	Issuer string
	Node   ast.Node // Original node that triggered the mutation
	Stmt   ast.Stmt // Enclosing statement
	NewStr string   // Statement source after mutation
	OldStr string   // Statement source before mutation
}

type Mutator interface {
	replacement(string, ast.Node, []ast.Node) *Replacement
}

type ChangeMode int

const (
	SC_APPEND ChangeMode = iota
	SC_REPLACE
	SC_DELETE
)

type Mutation struct {
	File   *FileInfo
	Change *Replacement
	Pos    token.Pos
}

type FileInfo struct {
	Id      int
	Path    string
	Source  []byte
	Package *PackageInfo
	AST     *ast.File
	Imports map[string]bool
	// Mutations per block
	Mutations map[token.Pos][]*Mutation
	Changes   map[token.Pos]*SourceChange
}

// Even parsing with comments still can mess compiler directives
// This is a way to change the ast without moving anything else
type SourceChange struct {
	Mode ChangeMode
	Code string
	End  token.Pos
}

const (
	MUTATION_NUMBER  = 1000
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

// Yao, Xiangjuan; Harman, Mark; Jia, Yue  (2014).
// [ACM Press the 36th International Conference - Hyderabad, India (2014.05.31-2014.06.07)]
// Proceedings of the 36th International Conference on Software Engineering
// - ICSE 2014 - A study of equivalent and stubborn mutation operators using human analysis of equivalence.
// , (), 919–930.
// doi:10.1145/2568225.2568265
var (
	DEFAULT_MUTATORS = []Mutator{
		UOIIncrementer{},
		UOIDecrementer{},

		AORMinusToDiv{},
		AORModToAdd{},
		AORModToSub{},
	}
	TMP_ROOT string
	FLAGS    = map[string]string{
		"verbose": "true",
	}
)

// FIXME: Don't use an external command
func packageInfo(cfg Config) []PackageInfo {
	// Output will be a series of lines
	// each line will have a semicolon separated list of:
	// Dir ; ImportPath ; GoFiles ; TestGofiles
	// GoFiles and TestGofiles are Comma Separated Values

	Verbosef("AT (%s) EXEC go list -f {{.Dir}};{{.ImportPath}};{{range .GoFiles}}{{.}},{{end}};{{range .TestGoFiles}}{{.}},{{end}} ./...\n", TMP_ROOT)
	cmd := exec.Command(
		"go", "list", "-f",
		"{{.Dir}};{{.ImportPath}};{{range .GoFiles}}{{.}},{{end}};{{range .TestGoFiles}}{{.}},{{end}}",
		cfg.Package,
	)
	cmd.Dir = TMP_ROOT
	out, err := cmd.Output()

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

// Prints to stdout only if the global verbose flag is set
func Verbosef(msg string, args ...interface{}) {
	if FLAGS["verbose"] != "true" {
		return
	}

	fmt.Printf(msg, args...)
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

func RootStmt(path []ast.Node) ast.Stmt {
	for i := len(path) - 1; i >= 0; i-- {
		node := path[i]
		if stmt, ok := node.(ast.Stmt); ok {
			return stmt
		}
	}
	return nil
}

type UOIIncrementer struct{}

func MutationString(source string, stmt ast.Node, og ast.Node, new ast.Node) (string, string) {
	writer := bytes.Buffer{}
	fs := token.NewFileSet()
	for i := stmt.Pos(); i < stmt.End(); i++ {
		c := source[i]
		if i == og.Pos() {
			printer.Fprint(&writer, fs, new)
			i = og.End() - 1
		} else {
			writer.WriteByte(c)
		}
	}

	return source[stmt.Pos():stmt.End()], writer.String()
}

func (UOIIncrementer) replacement(source string, orig ast.Node, path []ast.Node) *Replacement {
	node, ok := orig.(*ast.BasicLit)
	if !ok {
		return nil
	}
	if node.Kind != token.FLOAT && node.Kind != token.INT {
		return nil
	}

	stmt := RootStmt(path)
	if stmt == nil {
		return nil
	}

	new := &ast.BinaryExpr{X: node, OpPos: token.NoPos, Op: token.ADD, Y: &ONE}
	newStr, oldStr := MutationString(source, stmt, orig, new)
	return &Replacement{"UOIIncrementer", orig, stmt, newStr, oldStr}
}

type UOIDecrementer struct{}

func (UOIDecrementer) replacement(source string, orig ast.Node, path []ast.Node) *Replacement {
	node, ok := orig.(*ast.BasicLit)
	if !ok {
		return nil
	}
	if node.Kind != token.FLOAT && node.Kind != token.INT {
		return nil
	}

	stmt := RootStmt(path)
	if stmt == nil {
		return nil
	}

	new := &ast.BinaryExpr{X: node, OpPos: token.NoPos, Op: token.SUB, Y: &ONE}
	newStr, oldStr := MutationString(source, stmt, orig, new)
	return &Replacement{"UOIDecrementer", orig, stmt, newStr, oldStr}
}

func operatorReplacement(issuer string, source string, path []ast.Node, orig ast.Node, op token.Token, newOp token.Token) *Replacement {
	node, ok := orig.(*ast.BinaryExpr)
	if !ok || node.Op != op {
		return nil
	}

	stmt := RootStmt(path)
	if stmt == nil {
		return nil
	}

	new := &ast.BinaryExpr{X: node.X, OpPos: token.NoPos, Op: newOp, Y: node.Y}
	newStr, oldStr := MutationString(source, stmt, orig, new)
	return &Replacement{issuer, orig, stmt, newStr, oldStr}
}

type AORMinusToDiv struct{}

func (AORMinusToDiv) replacement(source string, orig ast.Node, path []ast.Node) *Replacement {
	return operatorReplacement("AORMinusToDiv", source, path, orig, token.SUB, token.QUO)
}

type AORModToAdd struct{}

func (AORModToAdd) replacement(source string, orig ast.Node, path []ast.Node) *Replacement {
	return operatorReplacement("AORModToAdd", source, path, orig, token.REM, token.ADD)
}

type AORModToSub struct{}

func (AORModToSub) replacement(source string, orig ast.Node, path []ast.Node) *Replacement {
	return operatorReplacement("AORModToSub", source, path, orig, token.REM, token.SUB)
}

// func schemata[T any](toggle string, origEx T, replaceEx ...T) T {
// 	return origEx
// }

func mutations(source string, node ast.Node, path []ast.Node, mutators []Mutator) []*Replacement {
	changes := []*Replacement{}
	for _, mut := range mutators {
		change := mut.replacement(source, node, path)
		if change != nil {
			changes = append(changes, change)
		}
	}
	return changes
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
	astWalk := func(node ast.Node) (ret bool) {
		ret = true

		if node == nil {
			path = path[:len(path)-1]
			return
		}
		path = append(path, node)

		// If this node has mutations we will instrument the enclosing block to check reachability at runtime
		changes := mutations(string(file.Source), node, path, DEFAULT_MUTATORS)
		if len(changes) == 0 {
			return
		}

		parentNode, at := getParent(path)
		if parentNode == nil {
			return
		}

		// Here we append mutations to the parent block scope
		muts := []*Mutation{}
		for _, change := range changes {
			// Add the mutation with the actual location
			muts = append(muts, &Mutation{file, change, node.Pos()})
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

// Adds the __reach function on the file at goFilePath
func (ft *FileTable) NewFileInfo(path string, pack *PackageInfo) *FileInfo {
	var fs token.FileSet
	var err error

	file := FileInfo{
		Path: path, Package: pack, Changes: make(map[token.Pos]*SourceChange),
		Imports: make(map[string]bool), Mutations: make(map[token.Pos][]*Mutation),
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

	file.Id = len(ft.Files)
	ft.Files = append(ft.Files, &file)
	return &file
}

type FileTable struct {
	Files []*FileInfo
}

// Returns a map of (blockLocation => testLocations[])
// blockLocation is the location of the parentBlock of one or more mutations
func ParseCoverage(source string) map[NodeIdentifier][]NodeIdentifier {
	var currentTest NodeIdentifier

	testsPerBlock := make(map[NodeIdentifier][]NodeIdentifier)
	for _, line := range strings.Split(source, "\n") {
		info := strings.Split(line, " ")
		// info[0] (Tag) = T or R
		// info[1] (Node Identifier) = fileId:NodePos
		if len(info) != 2 {
			continue
		}

		ident := strings.Split(info[1], ":")
		if len(ident) != 2 {
			fmt.Println("Malformed coverage data 2")
			os.Exit(1)
		}
		fileId, _ := strconv.ParseInt(ident[0], 10, 64)
		nodePos, _ := strconv.ParseInt(ident[1], 10, 64)

		nodeIdentifier := NodeIdentifier{int(fileId), int(nodePos)}
		if info[0] == "T" {
			currentTest = nodeIdentifier
		} else if info[0] == "R" {
			testsPerBlock[nodeIdentifier] = append(testsPerBlock[nodeIdentifier], currentTest)
		}
	}
	return testsPerBlock
}

type NodeIdentifier struct {
	FileId  int
	NodePos int
}

func GolangMut(cfg Config) {
	var coverageData string = ""
	ft := FileTable{}

	// Get all packages at the cfg.Package path
	packages := packageInfo(cfg)

	// Use a given coverage file
	if cfg.CoverageFile != "" {
		coverage, err := os.ReadFile(cfg.CoverageFile)
		coverageData = string(coverage)
		if err != nil {
			panic(err)
		}
	}

	if cfg.Nocov {
		panic("not implemented")
	}
	// For each package, add their files to FileTable
	for _, pkg := range packages {
		// If the package has no tests, or is empty: skip
		if len(pkg.TestGoFiles) == 0 {
			fmt.Printf("?\t%s\t[no test files]\n", pkg.ImportPath)
			continue
		} else if len(pkg.GoFiles) == 0 {
			fmt.Printf("?\t%s\t[no .go files]\n", pkg.ImportPath)
			continue
		}

		// For each Source file
		for _, source := range pkg.GoFiles {
			file := ft.NewFileInfo(pkg.Dir+"/"+source, &pkg)
			// Add instrumentation code to compute coverage
			file.addInstrumentationGo()
			file.writeInstrumentation()
		}
		for _, source := range pkg.TestGoFiles {
			file := ft.NewFileInfo(pkg.Dir+"/"+source, &pkg)
			// Add instrumentation code to compute coverage
			file.addInstrumentationTEST()
			file.writeInstrumentation()
		}

		// Bail if already has coverage data
		if coverageData != "" {
			continue
		}

		// Run tests of package to get coverage data
		fmt.Println("computing coverage >> go test " + pkg.ImportPath)
		err := exec.Command("go", "test", pkg.ImportPath).Run()
		if err != nil {
			panic(err)
		}
	}

	// If not provided with coverage file, ensure one was generated
	if coverageData == "" {
		reach, err := os.ReadFile(filepath.Join(TMP_ROOT, "reach.log"))
		coverageData = string(reach)
		if err != nil {
			panic(err)
		}
	}

	// For each parent block of a mutation we have the tests that reached it
	testsPerBlock := ParseCoverage(coverageData)

	// Get all reachable mutations
	reachableMutations := []*Mutation{}
	for block := range testsPerBlock {
		file := ft.Files[block.FileId]
		reachableMutations = append(reachableMutations, file.Mutations[token.Pos(block.NodePos)]...)
	}

	// Select mutations a random fixed number of mutations
	// https://doi.org/10.1109/ISSRE.2015.7381815
	rand.Shuffle(len(reachableMutations), func(i, j int) {
		reachableMutations[i], reachableMutations[j] = reachableMutations[j], reachableMutations[i]
	})

	slice := MUTATION_NUMBER
	if len(reachableMutations) < MUTATION_NUMBER {
		slice = len(reachableMutations)
	}

	selectedMutations := reachableMutations[:slice]

	// Get all mutations :'D
	allMutations := []*Mutation{}
	for _, file := range ft.Files {
		for _, muts := range file.Mutations {
			allMutations = append(allMutations, muts...)
		}
	}
	GenReport(allMutations, selectedMutations, reachableMutations)
}

func Mutation1(orig func(), mut func()) {
	if _, ok := os.LookupEnv("Mutation1"); ok {
		mut()
	} else {
		orig()
	}
}

func GenReport(all []*Mutation, selected []*Mutation, reachable []*Mutation) {
	report := make(map[string]any)
	report["totalMutations"] = len(all)
	report["reachableMutations"] = len(reachable)
	report["selectedMutations"] = len(selected)
	countByIssuer := make(map[string]int)
	for _, mut := range all {
		Mutation1(func() { countByIssuer[mut.Change.Issuer] += 1 }, func() { countByIssuer[mut.Change.Issuer] += 1 + 1 })
	}

	report["byOperator"] = countByIssuer
	res, _ := json.Marshal(report)
	fmt.Println(string(res))
}

type Config struct {
	Directory    string
	Package      string
	CoverageFile string
	Nocov        bool
}

var ROOT string

func main() {
	rand.Seed(time.Now().UnixNano())
	config := Config{}

	flag.BoolVar(&config.Nocov, "nocov", false, "skips getting coverage data")
	flag.StringVar(&config.Directory, "directory", "../kubectl", "project directory")
	flag.StringVar(&config.Package, "package", "./...", "package to run mutation analysis")
	flag.StringVar(&config.CoverageFile, "coverage", "reach.log", "file with previously collected coverage data")
	flag.Parse()

	wd, _ := exec.Command("pwd").Output()
	ROOT = string(wd)

	path := copyProject(config.Directory)
	TMP_ROOT = path
	defer removeProjectCopy(path)
	fmt.Println(path)
	GolangMut(config)
}
