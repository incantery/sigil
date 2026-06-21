; keywords (anonymous string nodes)
["let" "import" "as" "type" "fun" "if" "then" "else" "match" "with"] @keyword
; pub and rec are named nodes in this grammar
(pub) @keyword
(rec) @keyword

; literals
(string) @string
(interpolation ["${" "}"] @punctuation.special)
(number) @number
(float) @number.float
(line_comment) @comment

; types vs values (capitalization)
(uident) @type
(constructor name: (uident) @constructor)
(hole) @function.builtin

; bindings and calls
(let_decl name: (identifier) @function)
(parameter (identifier) @variable.parameter)
(field field: (identifier) @property)
(identifier) @variable

; operators & punctuation
["|>" "||" "&&" "==" "!=" "<" ">" "<=" ">=" "++" "+" "-" "*" "/" "->" "="] @operator
["(" ")" "[" "]" "}" "|" "," "."] @punctuation.delimiter
(wildcard) @variable.builtin
