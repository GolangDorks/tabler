package lib

import (
	"bytes"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/ioutil"
	"path/filepath"
	"strings"
)

func printableType(t string) string {
	t = strings.TrimPrefix(t, "&{")
	t = strings.TrimSuffix(t, "}")
	return strings.Replace(t, " ", ".", -1)
}

// InputFile is a go file with tables.
type InputFile struct {
	PackageName string
	BuildTarget string
	Tables      []Table
}

// Init initializes an InputFile from a path.
func (i *InputFile) Init(path string) error {

	// set the build target
	if strings.HasSuffix(path, ".go") {
		root := strings.TrimSuffix(path, ".go")
		dir, file := filepath.Split(root)
		(*i).BuildTarget = filepath.Join(dir, fmt.Sprintf("%s_tabler.go", file))
	} else {
		return fmt.Errorf("File '%s' is not a Go file.", path)
	}

	f, err := parser.ParseFile(
		token.NewFileSet(),
		path,
		nil,
		parser.ParseComments,
	)
	if err != nil {
		fmt.Errorf("Unable to parse '%s': %s", path, err)
	}

	// get package name
	if f.Name != nil {
		(*i).PackageName = f.Name.Name
	} else {
		fmt.Errorf("Missing package name in '%s'", path)
	}

	// build list of tables
	var isTable bool
	for _, decl := range f.Decls {

		// get the type declaration
		tdecl, ok := decl.(*ast.GenDecl)
		if !ok || tdecl.Doc == nil {
			continue
		}

		// find the @table decorator
		isTable = false
		for _, comment := range tdecl.Doc.List {
			if strings.Contains(comment.Text, "@table") {
				isTable = true
				break
			}
		}
		if !isTable {
			continue
		}

		table := Table{}

		// get the name of the table
		for _, spec := range tdecl.Specs {
			if ts, ok := spec.(*ast.TypeSpec); ok {
				if ts.Name == nil {
					continue
				}
				table.Name = ts.Name.Name
				break
			}
		}
		if table.Name == "" {
			return fmt.Errorf("Unable to extract name from a table struct.")
		}

		// parse the tabler tag and build columns
		sdecl := tdecl.Specs[0].(*ast.TypeSpec).Type.(*ast.StructType)
		for _, field := range sdecl.Fields.List {

			// get the printable type for each field
			var ftype string
			if starx, ok := field.Type.(*ast.StarExpr); ok {
				ftype = "*" + printableType(fmt.Sprintf("%v", starx.X))
			} else {
				ftype = printableType(fmt.Sprintf("%v", field.Type))
			}

			// check for a database reference
			if ftype == "*sql.DB" {
				table.HasConn = true
				table.Conn = field.Names[0].Name
			}

			// check for tabler tag
			if field.Tag != nil {
				match := tagPattern.FindStringSubmatch(field.Tag.Value)
				if len(match) == 2 {
					col := Column{}
					if err := col.init(field.Names[0].Name, ftype, match[1]); err != nil {
						return fmt.Errorf("Unable to parse tag '%s': %v", match[1], err)
					}
					table.Columns = append(table.Columns, col)
					if col.IsPrimary {
						table.PrimaryKeys = append(table.PrimaryKeys, col)
					}
				}
			}
		}

		// add the table if it has columns
		if len(table.Columns) > 0 {
			(*i).Tables = append((*i).Tables, table)
		}
	}

	return nil
}

func (i InputFile) Write() error {
	buf := bytes.Buffer{}
	tmpl := templify(`// generated by tabler
package {{.PackageName}}
{{range $j, $t := .Tables}}
// {{.Name}}

{{$t.CreateTable}}

{{$t.DropTable}}

{{$t.InsertRow}}

{{$t.SelectRow}}
{{end}}`)
	tmpl.Execute(&buf, i)

	return ioutil.WriteFile(i.BuildTarget, buf.Bytes(), 0644)
}
