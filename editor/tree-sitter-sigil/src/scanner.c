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
//     indent < top  → emit NEWLINE (if valid), then for each block level:
//                       DEDENT, NEWLINE (to terminate the enclosing statement),
//                     then one final NEWLINE for the outermost level
//   At EOF: same as shallower case (unwind to sentinel).
//
// Inside ( ) [ ] { } layout is fully suspended: no layout tokens emitted.
// Blank lines and `//`-comment-only lines are skipped when measuring indent.
//
// State machine (phase):
//   PHASE_NONE            — nothing pending, normal scan
//   PHASE_INDENT          — emit INDENT (after NEWLINE was emitted for deeper line)
//   PHASE_DEDENT          — emit DEDENT (then NEWLINE between consecutive DEDENTs)
//   PHASE_INTER_DEDENT    — emit NEWLINE between two consecutive DEDENTs
//   PHASE_POST_DEDENT     — emit one final NEWLINE after all DEDENTs are done

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
  PHASE_INTER_DEDENT,
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
//
// A stack entry is an indent column. The high bit (MATCH_BIT) marks a "match
// arm block": a block whose statements (arms) sit at the SAME column as the
// `match` that introduces them, delimited by leading `|`. Such a block is
// opened by an INDENT even when the next line is not deeper, and closed by a
// DEDENT when the next same-column line does not start with `|`.

#define MATCH_BIT 0x80000000u

static void s_push(Scanner *s, uint32_t v) {
  if (s->size == s->cap) {
    s->cap = s->cap ? s->cap * 2 : 8;
    s->stack = realloc(s->stack, s->cap * sizeof(uint32_t));
  }
  s->stack[s->size++] = v;
}

// raw top (including the MATCH_BIT marker).
static uint32_t s_top_raw(Scanner *s) {
  return s->size > 0 ? s->stack[s->size - 1] : 0;
}

// top indent column (MATCH_BIT masked off).
static uint32_t s_top(Scanner *s) {
  return s_top_raw(s) & ~MATCH_BIT;
}

static bool s_top_is_match(Scanner *s) {
  return (s_top_raw(s) & MATCH_BIT) != 0;
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
      } else {
        // More DEDENTs remain — emit an inter-DEDENT NEWLINE first so the
        // enclosing block's repeat(seq(_statement, _newline)) can close
        // before the next DEDENT fires.
        s->phase = PHASE_INTER_DEDENT;
      }
      lexer->result_symbol = DEDENT;
      return true;
    }
    // DEDENT not valid yet — preserve phase so tree-sitter retries.
    return false;

  case PHASE_INTER_DEDENT:
    // Emit NEWLINE between consecutive DEDENTs to terminate the enclosing
    // statement before closing the next outer block.
    if (valid_symbols[NEWLINE]) {
      s->phase = PHASE_DEDENT;
      lexer->result_symbol = NEWLINE;
      return true;
    }
    // NEWLINE not valid yet — preserve phase so tree-sitter retries.
    return false;

  case PHASE_POST_DEDENT:
    if (valid_symbols[NEWLINE]) {
      s->phase = PHASE_NONE;
      lexer->result_symbol = NEWLINE;
      return true;
    }
    // NEWLINE not valid yet — preserve phase so tree-sitter retries.
    return false;

  case PHASE_NONE:
    break;
  }

  // --- Step 2: update bracket depth / close match blocks at `)` `]` `}` ---
  {
    int32_t la = lexer->lookahead;

    // A same-column match-arm block can be the last thing inside a bracket:
    //   (match … with | A -> x | B -> y)
    // Its closing `)` arrives with no intervening newline, so the block's
    // DEDENT must be emitted here, before the bracket is consumed. The scanner
    // is invoked at the `)` with DEDENT valid (verified), so close one match
    // level per call until the grammar stops asking for DEDENT.
    if ((la == ')' || la == ']' || la == '}') &&
        s_top_is_match(s) && valid_symbols[DEDENT]) {
      s_pop(s);
      lexer->result_symbol = DEDENT;
      return true;       // do NOT consume the bracket yet; re-enter on next call
    }

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

    // Column of the newline itself: >0 means this line had real content before
    // the newline (a statement-terminating newline); 0 means a leading blank
    // line. We use this to distinguish the file's first statement separator
    // from a spurious leading NEWLINE that error recovery may request during
    // incremental reparse — without relying on `started`, which is only
    // committed once the scanner has produced a token (return true).
    bool has_content_before_nl = lexer->get_column(lexer) > 0;

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
    bool at_eof     = lexer->eof(lexer);
    uint32_t indent = at_eof ? 0 : (uint32_t)lexer->get_column(lexer);
    uint32_t top    = s_top(s);
    bool next_is_bar = !at_eof && lexer->lookahead == '|';

    // A match-arm block opens right after `with`: the grammar then wants an
    // INDENT and not a NEWLINE. Emit INDENT even when the arms are not deeper
    // (same column as `match`), marking the level so it can be closed on the
    // first non-`|` line. This is the only place a same-column INDENT is made.
    if (valid_symbols[INDENT] && !valid_symbols[NEWLINE] &&
        indent >= top && next_is_bar) {
      s->pending_indent = indent | MATCH_BIT;
      s->started = true;
      s_push(s, indent | MATCH_BIT);
      lexer->result_symbol = INDENT;
      return true;
    }

    // Inside a same-column match-arm block: a line that does NOT start with `|`
    // ends the block — close it with a DEDENT (the shallower path below then
    // handles any further levels). Fall through by treating the match level as
    // closed when the next line is not an arm.
    if (s_top_is_match(s) && indent == top && !next_is_bar) {
      s->pending_indent = indent;
      if (valid_symbols[NEWLINE]) {
        s->phase = PHASE_DEDENT;       // NEWLINE now, DEDENT(s) next
        lexer->result_symbol = NEWLINE;
        return true;
      }
      if (valid_symbols[DEDENT]) {
        s_pop(s);
        s->phase = (s_top(s) <= indent) ? PHASE_POST_DEDENT : PHASE_INTER_DEDENT;
        lexer->result_symbol = DEDENT;
        return true;
      }
      return false;
    }

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
      // Same level — statement separator (this includes same-column match arms,
      // which start with `|`). Emit NEWLINE whenever the parser wants one,
      // EXCEPT at the very start of the file (no real content seen yet): error
      // recovery during incremental reparse can spuriously mark NEWLINE valid at
      // a leading blank line, which would inject a stray NEWLINE before the
      // first statement. `has_content_before_nl` / `started` guard that.
      if ((s->started || has_content_before_nl || at_eof) &&
          valid_symbols[NEWLINE]) {
        s->started = true;
        lexer->result_symbol = NEWLINE;
        return true;
      }
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
      } else {
        // More DEDENTs remain — emit NEWLINE between them.
        s->phase = PHASE_INTER_DEDENT;
      }
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
      } else {
        s->phase = PHASE_INTER_DEDENT;
      }
      lexer->result_symbol = DEDENT;
      return true;
    }
  }

  return false;
}
