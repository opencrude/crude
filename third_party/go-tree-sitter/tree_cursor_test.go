package tree_sitter_test

import (
	"fmt"
	"testing"

	. "github.com/tree-sitter/go-tree-sitter"
	tree_sitter_go "github.com/tree-sitter/tree-sitter-go/bindings/go"
)

func ExampleTreeCursor() {
	parser := NewParser()
	defer parser.Close()

	language := NewLanguage(tree_sitter_go.Language())

	parser.SetLanguage(language)

	tree := parser.Parse(
		[]byte(`
			package main


			func main() {
				return
			}
		`),
		nil,
	)
	defer tree.Close()

	cursor := tree.Walk()
	defer cursor.Close()

	fmt.Println(cursor.Node().Kind())

	fmt.Println(cursor.GotoFirstChild())
	fmt.Println(cursor.Node().Kind())

	fmt.Println(cursor.GotoFirstChild())
	fmt.Println(cursor.Node().Kind())

	// Returns `false` because the `package` node has no children
	fmt.Println(cursor.GotoFirstChild())

	fmt.Println(cursor.GotoNextSibling())
	fmt.Println(cursor.Node().Kind())

	fmt.Println(cursor.GotoParent())
	fmt.Println(cursor.Node().Kind())

	fmt.Println(cursor.GotoNextSibling())
	fmt.Println(cursor.GotoNextSibling())
	fmt.Println(cursor.Node().Kind())

	// Output:
	// source_file
	// true
	// package_clause
	// true
	// package
	// false
	// true
	// package_identifier
	// true
	// package_clause
	// true
	// false
	// function_declaration
}

func TestTreeCursorNodeValueKeepsIdentityWithoutPerVisitAllocation(t *testing.T) {
	parser := NewParser()
	defer parser.Close()
	if err := parser.SetLanguage(NewLanguage(tree_sitter_go.Language())); err != nil {
		t.Fatal(err)
	}
	const source = "package p\nfunc f() { call(1) }\n"
	tree := parser.Parse([]byte(source), nil)
	if tree == nil {
		t.Fatal("parse failed")
	}
	defer tree.Close()
	cursor := tree.Walk()
	defer cursor.Close()
	saved := cursor.NodeValue()
	if allocations := testing.AllocsPerRun(100, func() {
		value := cursor.NodeValue()
		if value.StartByte() != 0 || value.EndByte() != uint(len(source)) || value.Id() != saved.Id() {
			t.Fatal("value changed current root")
		}
	}); allocations != 0 {
		t.Fatalf("current-node value allocated per visit: %g", allocations)
	}
	visits := 0
	for {
		value, pointer := cursor.NodeValue(), cursor.Node()
		if value.Id() != pointer.Id() || value.KindId() != pointer.KindId() || value.StartByte() != pointer.StartByte() || value.EndByte() != pointer.EndByte() {
			t.Fatal("value differs from existing node API")
		}
		visits++
		if cursor.GotoFirstChild() {
			continue
		}
		for !cursor.GotoNextSibling() {
			if !cursor.GotoParent() {
				if visits < 10 || saved.StartByte() != 0 || saved.EndByte() != uint(len(source)) {
					t.Fatal("traversal lost the saved root", visits)
				}
				return
			}
		}
	}
}
