{{if eq .Kind "review"}}verify{{else if eq .Kind "interrupted"}}retry{{else if eq .Kind "orphaned"}}resume{{else}}read; act{{end}}
