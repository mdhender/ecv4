# phrases
Phrases is a passphrase generator

# Usage

```go
package main

import (
    "fmt"
    "math/rand/v2"
	psg "github.com/mdhender/ecv3/internal/phrases"
)

func main() {
	r := rand.New(rand.NewPCG(19, 42))
	// the list of separators is optional
    fmt.Println(psg.Generate(r, 5, "."))
}
```

# Words
List is derived from https://www.eff.org/files/2016/09/08/eff_short_wordlist_1.txt.
The list generates about 10.3 bits of entropy per word.
To get 64 bits of entropy, we need at least 64 / 10.3 = 6.2 words.
