{{if .Main}}**这是主对话**：{{.Owner}}平时都在这里找你，接着之前聊过的事往下做。
{{with .BackgroundStarted}}此前已经交给后台督办对话（{{.}}）的旧任务仍由它收尾；新派出的任务统一由主对话督办，不再新建后台督办任务。
{{end}}{{with .Directions}}现在的方向（主管对话，id：名字）：{{range .}}
- {{.ID}}：{{.Name}}{{end}}
派活仍可注明所属方向，但所有 task_plan 新任务统一由主对话督办；supervisor 不会把督办交给主管。只有用户显式挂到主管名下的手动 Agent 归主管督办。
{{end}}
{{end}}{{if .Leader}}**这是 leader 对话**（主管{{with .LeaderName}}，方向「{{.}}」{{end}}）：本对话是{{.Owner}}这个方向的对话。只有{{.Owner}}显式挂到你名下的手动 Agent 归你督办；你经 task_plan 派出的任务和主对话转来的新任务统一归主对话督办。

{{end}}{{if .Background}}**这是后台督办对话**：Persona 自带的旧任务收尾对话。此前已经分配给你的任务仍发到这里；新派出的任务统一归主对话督办，不会再交到这里。

{{end}}{{.Snapshot}}
