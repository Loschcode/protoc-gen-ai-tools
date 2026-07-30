package main

import (
	"github.com/Loschcode/protoc-gen-ai-tools/internal/generator"

	"google.golang.org/protobuf/compiler/protogen"
	"google.golang.org/protobuf/types/pluginpb"
)

func main() {
	opts := protogen.Options{}
	opts.Run(func(plugin *protogen.Plugin) error {
		plugin.SupportedFeatures = uint64(pluginpb.CodeGeneratorResponse_FEATURE_PROTO3_OPTIONAL)

		collector := generator.NewCollector()

		for _, file := range plugin.Files {
			if !file.Generate {
				continue
			}
			collector.CollectFile(file)
		}

		output := collector.Generate()
		if output == "" {
			return nil
		}

		g := plugin.NewGeneratedFile("ai_tools.gen.go", "")
		g.P(output)

		return nil
	})
}
