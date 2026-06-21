// External scanner for Sigil's indent-sensitive block syntax.
//
// Emits three synthetic tokens in the order declared in grammar.js externals:
//   NEWLINE - end of a significant line
//   INDENT  - start of a deeper block
//   DEDENT  - end of a deeper block
//
// Layout rule:
//   On each significant newline, compare the next line's indent to the stack top:
//     indent > top  → emit NEWLINE (if valid), then INDENT on next call
//     indent == top → emit NEWLINE
//     indent < top  → emit NEWLINE (if valid), then DEDENT(s) on subsequent calls,
//                     then one final NEWLINE for the outer level
//   At EOF: same as shallower case (unwind to sentinel).
//
// Inside ( ) [ ] { } layout is fully suspended: no layout tokens emitted.
// Blank lines and `//`-comment-only lines are skipped when measuring indent.
//
// State machine (phase):
//   PHASE_NONE         — nothing pending, normal scan
//   PHASE_INDENT       — emit INDENT (after NEWLINE was emitted for deeper line)
//   PHASE_DEDENT       — emit DEDENT(s) (after NEWLINE was emitted for shallower line)
//   PHASE_POST_DEDENT  — emit one final NEWLINE after all DEDENTs are done

#include <tree_sitter/parser.h>
#include <stdlib.h>
#include <string.h>
#include <stdint.h>
#include <stdbool.h>

// Must match order in grammar.js externals: [$._newline, $._indent, $._dedent]
enum TokenType {
  NEWLINE,
  INDENT,
  DEDENT,
};

typedef enum {
  PHASE_NONE,
  PHASE_INDENT,
  PHASE_DEDENT,
  PHASE_POST_DEDENT,
} Phase;

typedef struct {
  uint32_t *stack;
  uint32_t  size;
  uint32_t  cap;
  uint32_t  pending_indent; // target indent when in PHASE_INDENT or PHASE_DEDENT
  Phase     phase;
  uint8_t   bracket_depth;
  bool      started;        // false until first real content seen (suppresses file-start NEWLINEs)
} Scanner;

// --- indent stack helpers ---

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

// --- tree-sitter external scanner API ---

void *tree_sitter_sigil_external_scanner_create(void) {
  Scanner *s = calloc(1, sizeof(Scanner));
  s_push(s, 0); // sentinel
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
  // Byte 0: phase (lower 4 bits) | started flag (bit 4)
  buffer[size++] = (char)((uint8_t)s->phase | (s->started ? 0x10 : 0));

  if (size + 1 > TREE_SITTER_SERIALIZATION_BUFFER_SIZE) return size;
  buffer[size++] = (char)s->bracket_depth;

  if (size + 4 > TREE_SITTER_SERIALIZATION_BUFFER_SIZE) return size;
  buffer[size++] = (char)( s->pending_indent        & 0xff);
  buffer[size++] = (char)((s->pending_indent >>  8) & 0xff);
  buffer[size++] = (char)((s->pending_indent >> 16) & 0xff);
  buffer[size++] = (char)((s->pending_indent >> 24) & 0xff);

  for (uint32_t i = 0; i < s->size; i++) {
    if (size + 4 > TREE_SITTER_SERIALIZATION_BUFFER_SIZE) break;
    uint32_t v = s->stack[i];
    buffer[size++] = (char)( v        & 0xff);
    buffer[size++] = (char)((v >>  8) & 0xff);
    buffer[size++] = (char)((v >> 16) & 0xff);
    buffer[size++] = (char)((v >> 24) & 0xff);
  }
  return size;
}

void tree_sitter_sigil_external_scanner_deserialize(void *payload,
                                                    const char *buffer,
                                                    unsigned length) {
  Scanner *s = (Scanner *)payload;
  s->size           = 0;
  s->phase          = PHASE_NONE;
  s->pending_indent = 0;
  s->bracket_depth  = 0;
  s->started        = false;

  unsigned i = 0;
  if (length >= 1) {
    uint8_t b  = (uint8_t)buffer[i++];
    s->phase   = (Phase)(b & 0x0f);
    s->started = (b & 0x10) != 0;
  }
  if (length >= 2) { s->bracket_depth = (uint8_t)buffer[i++]; }
  if (length >= i + 4) {
    s->pending_indent = (uint8_t)buffer[i]
                      | ((uint8_t)buffer[i+1] <<  8)
                      | ((uint8_t)buffer[i+2] << 16)
                      | ((uint8_t)buffer[i+3] << 24);
    i += 4;
  }
  while (length >= i + 4) {
    uint32_t v = (uint8_t)buffer[i]
               | ((uint8_t)buffer[i+1] <<  8)
               | ((uint8_t)buffer[i+2] << 16)
               | ((uint8_t)buffer[i+3] << 24);
    s_push(s, v);
    i += 4;
  }
  if (s->size == 0) s_push(s, 0); // ensure sentinel
}

bool tree_sitter_sigil_external_scanner_scan(void *payload, TSLexer *lexer,
                                             const bool *valid_symbols) {
  Scanner *s = (Scanner *)payload;

  // --- Step 1: drain queued phase tokens ---
  switch (s->phase) {

  case PHASE_INDENT:
    if (valid_symbols[INDENT]) {
      s_push(s, s->pending_indent);
      s->phase   = PHASE_NONE;
      s->started = true;
      lexer->result_symbol = INDENT;
      return true;
    }
    // INDENT not valid — grammar didn't want a block here; discard.
    s->phase = PHASE_NONE;
    break;

  case PHASE_DEDENT:
    if (valid_symbols[DEDENT]) {
      s_pop(s);
      if (s_top(s) <= s->pending_indent) {
        // Last DEDENT — schedule post-DEDENT NEWLINE for outer level.
        s->phase = PHASE_POST_DEDENT;
      }
      // else: more DEDENTs needed; stay in PHASE_DEDENT.
      lexer->result_symbol = DEDENT;
      return true;
    }
    // DEDENT not valid yet — caller will come back.
    s->phase = PHASE_NONE;
    break;

  case PHASE_POST_DEDENT:
    if (valid_symbols[NEWLINE]) {
      s->phase = PHASE_NONE;
      lexer->result_symbol = NEWLINE;
      return true;
    }
    s->phase = PHASE_NONE;
    break;

  case PHASE_NONE:
    break;
  }

  // --- Step 2: update bracket depth (no token emitted) ---
  {
    int32_t la = lexer->lookahead;
    if (la == '(' || la == '[' || la == '{') {
      if (s->bracket_depth < 255) s->bracket_depth++;
      return false;
    }
    if (la == ')' || la == ']' || la == '}') {
      if (s->bracket_depth > 0) s->bracket_depth--;
      return false;
    }
  }

  // --- Step 3: handle newline (layout trigger) ---
  if (s->bracket_depth == 0 &&
      (lexer->lookahead == '\n' || lexer->lookahead == '\r')) {

    // Consume newlines.
    while (lexer->lookahead == '\n' || lexer->lookahead == '\r') {
      lexer->advance(lexer, true);
    }

    // Skip blank lines and `//` comment-only lines.
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
      if (lexer->lookahead == '/') {
        lexer->advance(lexer, true);
        if (lexer->lookahead == '/') {
          while (lexer->lookahead != 0 && lexer->lookahead != '\n' &&
                 lexer->lookahead != '\r') {
            lexer->advance(lexer, true);
          }
          while (lexer->lookahead == '\n' || lexer->lookahead == '\r') {
            lexer->advance(lexer, true);
          }
          continue;
        }
        return false; // stray '/' — let normal lexer handle it
      }
      break;
    }

    // At EOF or at a significant character: measure indent.
    uint32_t indent = lexer->eof(lexer) ? 0 : (uint32_t)lexer->get_column(lexer);
    uint32_t top    = s_top(s);

    if (indent > top) {
      // Deeper indent — open a block.
      s->pending_indent = indent;
      s->started = true;
      if (valid_symbols[NEWLINE]) {
        s->phase = PHASE_INDENT;       // emit INDENT on next call
        lexer->result_symbol = NEWLINE;
        return true;
      }
      if (valid_symbols[INDENT]) {
        s_push(s, indent);             // emit INDENT immediately (no NEWLINE wanted)
        lexer->result_symbol = INDENT;
        return true;
      }
      return false;
    }

    if (indent == top) {
      // Same level — statement separator.
      // Suppress NEWLINE at the very start of the file (nothing parsed yet),
      // UNLESS we're at EOF (where we must close any open statement).
      bool at_eof = lexer->eof(lexer);
      if ((s->started || at_eof) && valid_symbols[NEWLINE]) {
        s->started = true;
        lexer->result_symbol = NEWLINE;
        return true;
      }
      // Mark that we've processed at least one newline (even if suppressed).
      // This prevents infinite suppression if the file has multiple blank lines
      // before the first statement.
      s->started = true;
      return false;
    }

    // Shallower indent (or EOF): close block(s).
    s->pending_indent = indent;
    if (valid_symbols[NEWLINE]) {
      // Emit NEWLINE first (terminates the inner statement), then DEDENT(s) next.
      s->phase = PHASE_DEDENT;
      lexer->result_symbol = NEWLINE;
      return true;
    }
    if (valid_symbols[DEDENT]) {
      s_pop(s);
      if (s_top(s) <= indent) {
        s->phase = PHASE_POST_DEDENT;
      }
      // else: more DEDENTs needed; stay in PHASE_DEDENT.
      lexer->result_symbol = DEDENT;
      return true;
    }
    return false;
  }

  // --- Step 4: EOF without a preceding newline ---
  if (lexer->eof(lexer)) {
    if (valid_symbols[NEWLINE] && s->size > 1) {
      // Emit NEWLINE first to close the innermost statement, then unwind blocks.
      s->pending_indent = 0;
      s->phase = PHASE_DEDENT;
      lexer->result_symbol = NEWLINE;
      return true;
    }
    if (s->size > 1 && valid_symbols[DEDENT]) {
      s_pop(s);
      if (s_top(s) <= 0) {
        s->phase = PHASE_POST_DEDENT;
      }
      lexer->result_symbol = DEDENT;
      return true;
    }
  }

  return false;
}
