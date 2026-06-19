// External scanner for Sigil's indent-sensitive block syntax.
//
// Emits three synthetic tokens:
//   NEWLINE - end of a significant line
//   INDENT  - start of a deeper block
//   DEDENT  - end of a deeper block
//
// Blank lines and `//` comment-only lines are skipped before computing the
// indent of the next significant line, so they don't open/close blocks.
//
// NOTE: comment-only lines are CONSUMED by this scanner — their text is
// advanced past with the `skip` flag set, so the normal lexer never sees
// them and they do NOT appear in the syntax tree. Trailing end-of-line
// comments (on the same line as content) are preserved as `comment` extras.
// If we ever want comment-only lines in the tree (for hover documentation
// at a given line, etc.), the scanner needs to emit them via an external
// `comment` token instead of swallowing them.
//
// State across incremental parses is just the indent stack — serialized as
// little-endian uint32 values, plus a one-byte flag for the pending-newline
// state (true when a NEWLINE was just emitted and the next call should emit
// INDENT or DEDENT(s) based on `pending_indent`).

#include <tree_sitter/parser.h>
#include <stdlib.h>
#include <string.h>

enum TokenType {
  NEWLINE,
  INDENT,
  DEDENT,
  RAW_BLOCK, // verbatim body of a `code` block (must match grammar externals)
};

typedef struct {
  uint32_t *stack;
  uint32_t size;
  uint32_t cap;
  uint32_t pending_indent;
  bool has_pending;
  bool emitted_eof_newline;
} Scanner;

static void s_push(Scanner *s, uint32_t v) {
  if (s->size == s->cap) {
    s->cap = s->cap ? s->cap * 2 : 8;
    s->stack = realloc(s->stack, s->cap * sizeof(uint32_t));
  }
  s->stack[s->size++] = v;
}

static uint32_t s_top(Scanner *s) {
  return s->size > 0 ? s->stack[s->size - 1] : 0;
}

static void s_pop(Scanner *s) {
  if (s->size > 1) s->size--;
}

void *tree_sitter_sigil_external_scanner_create(void) {
  Scanner *s = calloc(1, sizeof(Scanner));
  s_push(s, 0);
  return s;
}

void tree_sitter_sigil_external_scanner_destroy(void *payload) {
  Scanner *s = (Scanner *)payload;
  free(s->stack);
  free(s);
}

unsigned tree_sitter_sigil_external_scanner_serialize(void *payload, char *buffer) {
  Scanner *s = (Scanner *)payload;
  unsigned size = 0;
  if (size + 1 > TREE_SITTER_SERIALIZATION_BUFFER_SIZE) return size;
  buffer[size++] = (char)(s->has_pending ? 1 : 0)
                 | (char)(s->emitted_eof_newline ? 2 : 0);
  if (size + 4 > TREE_SITTER_SERIALIZATION_BUFFER_SIZE) return size;
  buffer[size++] = s->pending_indent       & 0xff;
  buffer[size++] = (s->pending_indent>>8)  & 0xff;
  buffer[size++] = (s->pending_indent>>16) & 0xff;
  buffer[size++] = (s->pending_indent>>24) & 0xff;
  for (uint32_t i = 0; i < s->size; i++) {
    if (size + 4 > TREE_SITTER_SERIALIZATION_BUFFER_SIZE) break;
    uint32_t v = s->stack[i];
    buffer[size++] = v        & 0xff;
    buffer[size++] = (v>>8)   & 0xff;
    buffer[size++] = (v>>16)  & 0xff;
    buffer[size++] = (v>>24)  & 0xff;
  }
  return size;
}

void tree_sitter_sigil_external_scanner_deserialize(void *payload, const char *buffer, unsigned length) {
  Scanner *s = (Scanner *)payload;
  s->size = 0;
  s->has_pending = false;
  s->emitted_eof_newline = false;
  s->pending_indent = 0;
  unsigned i = 0;
  if (length >= 1) {
    s->has_pending         = (buffer[i]   & 1) != 0;
    s->emitted_eof_newline = (buffer[i++] & 2) != 0;
  }
  if (length >= i + 4) {
    s->pending_indent = (uint8_t)buffer[i]
                     | ((uint8_t)buffer[i+1] << 8)
                     | ((uint8_t)buffer[i+2] << 16)
                     | ((uint8_t)buffer[i+3] << 24);
    i += 4;
  }
  while (i + 3 < length) {
    uint32_t v = (uint8_t)buffer[i]
              | ((uint8_t)buffer[i+1] << 8)
              | ((uint8_t)buffer[i+2] << 16)
              | ((uint8_t)buffer[i+3] << 24);
    s_push(s, v);
    i += 4;
  }
  if (s->size == 0) s_push(s, 0);
}

// Resolve a pending indent change by emitting one INDENT or DEDENT and
// updating the stack. Returns true if a token was emitted; clears
// `has_pending` once we've reached the matching level.
static bool resolve_pending(Scanner *s, TSLexer *lexer, const bool *valid_symbols) {
  uint32_t indent = s->pending_indent;
  uint32_t top = s_top(s);
  if (indent > top) {
    if (!valid_symbols[INDENT]) return false;
    s_push(s, indent);
    s->has_pending = false;
    lexer->result_symbol = INDENT;
    return true;
  }
  if (indent < top) {
    if (!valid_symbols[DEDENT]) return false;
    s_pop(s);
    if (s_top(s) <= indent) s->has_pending = false;
    lexer->result_symbol = DEDENT;
    return true;
  }
  // Equal — nothing left to do.
  s->has_pending = false;
  return false;
}

// scan_raw_block consumes the verbatim body of a `code` block. It runs right
// after the `code` line's NEWLINE — a spot where the indent machinery would
// normally open an INDENT block, but the grammar (code_block) instead expects
// raw_block. At entry the lexer sits at the first content char of the first
// body line (the preceding NEWLINE scan already skipped that line's
// indentation), and the pending indent it stashed is the body's indent.
//
// base is the `code` line's own indent (top of the indent stack). Every line
// indented deeper than base — blank lines included — joins the body. The
// token ends at the last body line, leaving its newline for the NEWLINE scan
// to reconcile the dedent back to base.
static bool scan_raw_block(Scanner *s, TSLexer *lexer) {
  uint32_t base = s_top(s);
  // We take ownership of the pending indent the NEWLINE scan stashed: the body
  // never opens an INDENT block of its own.
  s->has_pending = false;

  // No body (the first "body" line is already a dedent or EOF): not a block.
  if (lexer->eof(lexer)) {
    return false;
  }

  bool any = false;
  for (;;) {
    // Consume this body line's content (lexer is at its first content char).
    while (lexer->lookahead != '\n' && lexer->lookahead != 0) {
      lexer->advance(lexer, false);
      any = true;
    }
    lexer->mark_end(lexer);

    // Advance to the next non-blank line; stop at a dedent or EOF.
    bool more = false;
    for (;;) {
      while (lexer->lookahead == '\n' || lexer->lookahead == '\r') {
        lexer->advance(lexer, true);
      }
      if (lexer->lookahead == 0) break;
      while (lexer->lookahead == ' ' || lexer->lookahead == '\t') {
        lexer->advance(lexer, true);
      }
      if (lexer->lookahead == '\n' || lexer->lookahead == '\r') continue; // blank
      if (lexer->lookahead == 0) break;
      if (lexer->get_column(lexer) > base) more = true;
      break;
    }
    if (!more) break;
  }

  if (!any) return false; // `code` with an empty body is not a raw_block.
  lexer->result_symbol = RAW_BLOCK;
  return true;
}

bool tree_sitter_sigil_external_scanner_scan(void *payload, TSLexer *lexer, const bool *valid_symbols) {
  Scanner *s = (Scanner *)payload;

  // Step 0: a `code` block's verbatim body. raw_block is grammatically valid
  // only right after the `code` line's newline, where the parser expects the
  // body instead of an INDENT. (INDENT is never valid there, which is exactly
  // what distinguishes this from an ordinary block opening.)
  if (valid_symbols[RAW_BLOCK] && !valid_symbols[INDENT]) {
    if (scan_raw_block(s, lexer)) return true;
  }

  // Step 1: drain any pending INDENT/DEDENT from a previous NEWLINE.
  if (s->has_pending) {
    if (resolve_pending(s, lexer, valid_symbols)) return true;
    // If we couldn't resolve (e.g. neither symbol valid here), fall through.
  }

  // Step 2: at a newline, consume it + blank lines + comment-only lines,
  // measure the indent of the next significant line, emit NEWLINE, and stash
  // the indent so step 1 picks it up on the next call.
  if (lexer->lookahead == '\n' || lexer->lookahead == '\r') {
    while (lexer->lookahead == '\n' || lexer->lookahead == '\r') {
      lexer->advance(lexer, true);
    }
    for (;;) {
      while (lexer->lookahead == ' ' || lexer->lookahead == '\t') {
        lexer->advance(lexer, true);
      }
      if (lexer->lookahead == '\n' || lexer->lookahead == '\r') {
        while (lexer->lookahead == '\n' || lexer->lookahead == '\r') {
          lexer->advance(lexer, true);
        }
        continue;
      }
      // `//` comment-only line: peek one char to confirm.
      if (lexer->lookahead == '/') {
        lexer->advance(lexer, true);
        if (lexer->lookahead == '/') {
          while (lexer->lookahead != 0 && lexer->lookahead != '\n') {
            lexer->advance(lexer, true);
          }
          continue;
        }
        // Stray `/` at line start — Sigil has no division/path syntax, so
        // this is malformed input. The `/` was consumed; the normal lexer
        // will see whatever follows. Treat the line as ending here.
        return false;
      }
      break;
    }

    uint32_t indent = lexer->get_column(lexer);

    if (lexer->eof(lexer)) {
      // EOF reached. Emit NEWLINE once (terminates the last logical line),
      // then DEDENTs until the stack is back to the sentinel.
      if (valid_symbols[NEWLINE] && !s->emitted_eof_newline) {
        s->emitted_eof_newline = true;
        s->has_pending = true;
        s->pending_indent = 0;
        lexer->result_symbol = NEWLINE;
        return true;
      }
      if (s->size > 1 && valid_symbols[DEDENT]) {
        s_pop(s);
        lexer->result_symbol = DEDENT;
        return true;
      }
      return false;
    }

    s->has_pending = true;
    s->pending_indent = indent;
    if (valid_symbols[NEWLINE]) {
      lexer->result_symbol = NEWLINE;
      return true;
    }
    // NEWLINE not expected at this point; let resolve_pending handle the
    // indent change directly.
    return resolve_pending(s, lexer, valid_symbols);
  }

  // Step 3: synthesize end-of-file dedents for the last open block.
  if (lexer->eof(lexer)) {
    if (valid_symbols[NEWLINE] && !s->emitted_eof_newline) {
      s->emitted_eof_newline = true;
      s->has_pending = true;
      s->pending_indent = 0;
      lexer->result_symbol = NEWLINE;
      return true;
    }
    if (s->size > 1 && valid_symbols[DEDENT]) {
      s_pop(s);
      lexer->result_symbol = DEDENT;
      return true;
    }
  }

  return false;
}
