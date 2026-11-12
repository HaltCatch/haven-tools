package main
import("encoding/json";"os";"github.com/HaltCatch/haven-tools/internal/toolkit")
func main(){ tools:=[]toolkit.Tool{}; for _,t:=range toolkit.Catalog(){if t.Mode=="browser"{tools=append(tools,t)}};if err:=json.NewEncoder(os.Stdout).Encode(map[string]any{"tools":tools});err!=nil{panic(err)}}
