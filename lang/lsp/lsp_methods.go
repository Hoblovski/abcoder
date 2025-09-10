// Copyright 2025 CloudWeGo Authors
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     https://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package lsp

import (
	"context"
	"fmt"
	"math"
	"os"
	"sort"

	"github.com/cloudwego/abcoder/lang/utils"
	lsp "github.com/sourcegraph/go-lsp"
)

type DocumentRange struct {
	TextDocument lsp.TextDocumentIdentifier `json:"textDocument"`
	Range        Range                      `json:"range"`
}

type SemanticTokensFullParams struct {
	TextDocument lsp.TextDocumentIdentifier `json:"textDocument"`
}

type DidOpenTextDocumentParams struct {
	TextDocument TextDocumentItem `json:"textDocument"`
}

func (cli *LSPClient) DidOpen(ctx context.Context, file DocumentURI) (*TextDocumentItem, error) {
	if f, ok := cli.files[file]; ok {
		return f, nil
	}
	text, err := os.ReadFile(file.File())
	if err != nil {
		return nil, err
	}
	f := &TextDocumentItem{
		URI:        DocumentURI(file),
		LanguageID: cli.Language.String(),
		Version:    1,
		Text:       string(text),
		LineCounts: utils.CountLines(string(text)),
	}
	cli.files[file] = f
	req := DidOpenTextDocumentParams{
		TextDocument: *f,
	}
	if err := cli.Notify(ctx, "textDocument/didOpen", req); err != nil {
		return nil, err
	}
	return f, nil
}

func (cli *LSPClient) DocumentSymbols(ctx context.Context, file DocumentURI) (map[Range]*DocumentSymbol, error) {
	// open file first
	f, err := cli.DidOpen(ctx, file)
	if err != nil {
		return nil, err
	}
	if f.Symbols != nil {
		return f.Symbols, nil
	}
	uri := lsp.DocumentURI(file)
	req := lsp.DocumentSymbolParams{
		TextDocument: lsp.TextDocumentIdentifier{
			URI: uri,
		},
	}
	var resp []DocumentSymbol
	if err := cli.Call(ctx, "textDocument/documentSymbol", req, &resp); err != nil {
		return nil, err
	}
	// cache symbols
	f.Symbols = make(map[Range]*DocumentSymbol, len(resp))
	for i := range resp {
		s := &resp[i]
		f.Symbols[s.Location.Range] = s
	}
	return f.Symbols, nil
}

func (cli *LSPClient) References(ctx context.Context, id Location) ([]Location, error) {
	if _, err := cli.DidOpen(ctx, id.URI); err != nil {
		return nil, err
	}
	uri := lsp.DocumentURI(id.URI)
	req := lsp.ReferenceParams{
		TextDocumentPositionParams: lsp.TextDocumentPositionParams{
			TextDocument: lsp.TextDocumentIdentifier{
				URI: uri,
			},
			Position: lsp.Position{
				Line:      id.Range.Start.Line,
				Character: id.Range.Start.Character + 1,
			},
		},
		Context: lsp.ReferenceContext{
			IncludeDeclaration: true,
		},
	}
	var resp []Location
	if err := cli.Call(ctx, "textDocument/references", req, &resp); err != nil {
		return nil, err
	}
	return resp, nil
}

func (cli *LSPClient) SemanticTokens(ctx context.Context, id Location) ([]Token, error) {
	syms, err := cli.DocumentSymbols(ctx, id.URI)
	if err != nil {
		return nil, err
	}
	sym := syms[id.Range]
	if sym != nil && sym.Tokens != nil {
		return sym.Tokens, nil
	}

	if cli.hasSemanticTokensRange {
		return cli.semanticTokensRange(ctx, id, sym)
	} else {
		return cli.semanticTokensFull(ctx, id, sym)
	}
}

func (cli *LSPClient) semanticTokensRange(ctx context.Context, id Location, sym *DocumentSymbol) ([]Token, error) {
	req := DocumentRange{
		TextDocument: lsp.TextDocumentIdentifier{
			URI: lsp.DocumentURI(id.URI),
		},
		Range: id.Range,
	}
	var resp SemanticTokens
	if err := cli.Call(ctx, "textDocument/semanticTokens/range", req, &resp); err != nil {
		return nil, err
	}
	toks := cli.parseTokens(resp, id.URI)
	if sym != nil {
		sym.Tokens = toks
	}
	return toks, nil
}

func (cli *LSPClient) semanticTokensFull(ctx context.Context, id Location, sym *DocumentSymbol) ([]Token, error) {
	f, ok := cli.files[id.URI]
	if !ok || f.Tokens == nil {
		req := SemanticTokensFullParams{
			TextDocument: lsp.TextDocumentIdentifier{
				URI: lsp.DocumentURI(id.URI),
			},
		}
		var resp SemanticTokens
		if err := cli.Call(ctx, "textDocument/semanticTokens/full", req, &resp); err != nil {
			return nil, err
		}
		f.Tokens = cli.parseTokens(resp, id.URI)
	}
	toks := cli.filterTokens(f.Tokens, id.Range)
	if sym != nil {
		sym.Tokens = toks
	}
	return toks, nil
}

func (cli *LSPClient) Definition(ctx context.Context, uri DocumentURI, pos Position) ([]Location, error) {
	// open file first
	f, err := cli.DidOpen(ctx, uri)
	if err != nil {
		return nil, err
	}
	if f.Definitions != nil {
		if locations, ok := f.Definitions[pos]; ok {
			return locations, nil
		}
	}

	// call
	req := lsp.TextDocumentPositionParams{
		TextDocument: lsp.TextDocumentIdentifier{
			URI: lsp.DocumentURI(uri),
		},
		Position: lsp.Position(pos),
	}
	var resp []Location
	if err := cli.Call(ctx, "textDocument/definition", req, &resp); err != nil {
		return nil, err
	}

	// cache definitions
	if f.Definitions == nil {
		f.Definitions = make(map[Position][]Location)
	}
	f.Definitions[pos] = resp
	return resp, nil
}

func (cli *LSPClient) TypeDefinition(ctx context.Context, uri DocumentURI, pos Position) ([]Location, error) {
	req := lsp.TextDocumentPositionParams{
		TextDocument: lsp.TextDocumentIdentifier{
			URI: lsp.DocumentURI(uri),
		},
		Position: lsp.Position(pos),
	}
	var resp []Location
	if err := cli.Call(ctx, "textDocument/typeDefinition", req, &resp); err != nil {
		return nil, err
	}
	return resp, nil
}

// read file and get the text of block of range
func (cli *LSPClient) Locate(id Location) (string, error) {
	f, ok := cli.files[id.URI]
	if !ok {
		// open file os
		fd, err := os.ReadFile(id.URI.File())
		if err != nil {
			return "", err
		}
		text := string(fd)
		f = &TextDocumentItem{
			URI:        DocumentURI(id.URI),
			LanguageID: cli.Language.String(),
			Version:    1,
			Text:       text,
			LineCounts: utils.CountLines(text),
		}
		cli.files[id.URI] = f
	}

	text := f.Text
	// get block text of range
	start := f.LineCounts[id.Range.Start.Line] + id.Range.Start.Character
	end := f.LineCounts[id.Range.End.Line] + id.Range.End.Character
	return text[start:end], nil
}

// get line text of pos
func (cli *LSPClient) Line(uri DocumentURI, pos int) string {
	f, ok := cli.files[uri]
	if !ok {
		// open file os
		fd, err := os.ReadFile(uri.File())
		if err != nil {
			return ""
		}
		text := string(fd)
		f = &TextDocumentItem{
			URI:        DocumentURI(uri),
			LanguageID: cli.Language.String(),
			Version:    1,
			Text:       text,
			LineCounts: utils.CountLines(text),
		}
		cli.files[uri] = f
	}
	if pos < 0 || pos >= len(f.LineCounts) {
		return ""
	}
	start := f.LineCounts[pos]
	end := len(f.Text)
	if pos+1 < len(f.LineCounts) {
		end = f.LineCounts[pos+1]
	}
	return f.Text[start:end]
}

func (cli *LSPClient) LineCounts(uri DocumentURI) []int {
	f, ok := cli.files[uri]
	if !ok {
		// open file os
		fd, err := os.ReadFile(uri.File())
		if err != nil {
			return nil
		}
		text := string(fd)
		f = &TextDocumentItem{
			URI:        DocumentURI(uri),
			LanguageID: cli.Language.String(),
			Version:    1,
			Text:       text,
			LineCounts: utils.CountLines(text),
		}
		cli.files[uri] = f
	}
	return f.LineCounts
}

func (cli *LSPClient) GetFile(uri DocumentURI) *TextDocumentItem {
	return cli.files[uri]
}

func (cli *LSPClient) GetParent(sym *DocumentSymbol) (ret *DocumentSymbol) {
	if sym == nil {
		return nil
	}
	if f, ok := cli.files[sym.Location.URI]; ok {
		for _, s := range f.Symbols {
			if s != sym && s.Location.Range.Include(sym.Location.Range) {
				if ret == nil || ret.Location.Range.Include(s.Location.Range) {
					ret = s
				}
			}
		}
	}
	return
}

func (cli *LSPClient) filterTokens(tokens []Token, range_ Range) []Token {
	if len(tokens) == 0 {
		return []Token{}
	}
	// tokens.Location.Range shall not overlap.
	// tokens.Location.URI shall be the same (as range_).

	// bisect to find left
	l, r := 0, len(tokens)-1
	for l < r {
		mid := (l + r) / 2
		midRange := tokens[mid].Location.Range
		if midRange.Start.Less(range_.Start) {
			// discard until mid, discard mid (FALSE== mid.Start>=range_.Start)
			l = mid + 1
		} else {
			// discard after mid, keep mid (TRUE== mid.Start>=range_.Start)
			r = mid
		}
	}
	leftIndex := l
	// leftIndex: the first tok such that tok.Start >= range_.Start
	// all tokens before leftIndex are out of range
	// ---
	// bisect to find right
	l, r = 0, len(tokens)-1 // interval: [l, r] inclusive x2
	for l < r {
		mid := (l + r) / 2
		midRange := tokens[mid].Location.Range
		if midRange.Start.Less(range_.End) {
			// (FALSE== mid.Start>=range_.End)
			l = mid + 1
		} else {
			r = mid
		}
	}
	rightIndex := l
	// rightIndex: the first tok such that tok.Start >= range_.End
	// all tokens at or after rightIndex are out of range
	// ---
	for rightIndex >= leftIndex && !range_.Include(tokens[rightIndex].Location.Range) {
		rightIndex -= 1
	}
	// rightIndex: the last tok such that range_.Include(tok)
	res := tokens[leftIndex : rightIndex+1]
	return res
}

func (cli *LSPClient) parseTokens(tokens SemanticTokens, file DocumentURI) []Token {
	start := Position{Line: 0, Character: 0}
	end := Position{Line: math.MaxInt32, Character: math.MaxInt32}
	return cli.parseTokensRange(tokens, file, Range{Start: start, End: end})
}

func (cli *LSPClient) parseTokensRange(raw_tokens SemanticTokens, file DocumentURI, r Range) []Token {
	tokens := make([]Token, 0, len(raw_tokens.Data)/5)
	line := 0
	character := 0

	for i := 0; i < len(raw_tokens.Data); i += 5 {
		deltaLine := int(raw_tokens.Data[i])
		deltaStart := int(raw_tokens.Data[i+1])
		length := int(raw_tokens.Data[i+2])
		tokenType := int(raw_tokens.Data[i+3])
		tokenModifiersBitset := int(raw_tokens.Data[i+4])

		line += deltaLine
		if deltaLine == 0 {
			character += deltaStart
		} else {
			character = deltaStart
		}

		start := Position{Line: line, Character: character}
		if isPositionInRange(start, r, false) {
			tokenTypeName := getSemanticTokenType(tokenType, cli.tokenTypes)
			tokenModifierNames := getSemanticTokenModifier(tokenModifiersBitset, cli.tokenModifiers)
			end := Position{Line: line, Character: character + length}
			range_ := Range{Start: start, End: end}
			loc := Location{URI: file, Range: range_}
			text, _ := cli.Locate(loc)
			tokens = append(tokens, Token{
				Location:  loc,
				Type:      tokenTypeName,
				Modifiers: tokenModifierNames,
				Text:      text,
			})
		}
	}

	return tokens
}

func (cli *LSPClient) FileStructure(ctx context.Context, file DocumentURI) ([]*DocumentSymbol, error) {
	syms, err := cli.DocumentSymbols(ctx, file)
	if err != nil {
		return nil, err
	}
	// construct symbol hierarchy through range relation, and represent it to DocumentSymobl.Children
	symbols := make([]*DocumentSymbol, 0, len(syms))
	for _, sym := range syms {
		symbols = append(symbols, sym)
	}
	return constructSymbolHierarchy(symbols), nil
}

func getSemanticTokenType(id int, semanticTokenTypes []string) string {
	if id < len(semanticTokenTypes) {
		return semanticTokenTypes[id]
	}
	return fmt.Sprintf("unknown(%d)", id)
}

func getSemanticTokenModifier(bitset int, semanticTokenModifiers []string) []string {
	var result []string
	for i, modifier := range semanticTokenModifiers {
		if bitset&(1<<uint(i)) != 0 {
			result = append(result, modifier)
		}
	}
	for i := len(semanticTokenModifiers); i < 32; i++ {
		if bitset&(1<<uint(i)) != 0 {
			result = append(result, fmt.Sprintf("unknown(%d)", i))
		}
	}
	return result
}

// constructSymbolHierarchy constructs a symbol hierarchy through range relation and represents it in DocumentSymbol.Children.
func constructSymbolHierarchy(symbols []*DocumentSymbol) []*DocumentSymbol {
	// Sort symbols by their start position
	sort.Slice(symbols, func(i, j int) bool {
		if symbols[i].Location.Range.Start.Line == symbols[j].Location.Range.Start.Line {
			return symbols[i].Location.Range.Start.Character < symbols[j].Location.Range.Start.Character
		}
		return symbols[i].Location.Range.Start.Line < symbols[j].Location.Range.Start.Line
	})

	var rootSymbols []*DocumentSymbol
	var stack []*DocumentSymbol

	for i := range symbols {
		symbol := symbols[i]

		// Pop symbols from the stack that are not parents of the current symbol
		for len(stack) > 0 && !stack[len(stack)-1].Location.Range.Include(symbol.Location.Range) {
			stack = stack[:len(stack)-1]
		}

		// If the stack is not empty, the top symbol is the parent of the current symbol
		if len(stack) > 0 {
			parent := stack[len(stack)-1]
			parent.Children = append(parent.Children, symbol)
		} else {
			// If the stack is empty, the current symbol is a root symbol
			rootSymbols = append(rootSymbols, symbol)
		}

		// Push the current symbol onto the stack
		stack = append(stack, symbol)
	}

	return rootSymbols
}
