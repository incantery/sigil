// std/string — string utilities over the kernel's __split primitive.

// split breaks s into the pieces separated by sep.
pub let split s sep = __split s sep

// chars breaks s into its individual characters.
pub let chars s = __split s ""
