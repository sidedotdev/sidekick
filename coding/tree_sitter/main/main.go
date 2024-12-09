package main

import (
	"fmt"
	"sidekick/coding"
	"sidekick/coding/lsp"
	"sidekick/env"

	"github.com/joho/godotenv"
)

// str, err := tree_sitter.GetSymbolDefinition("./coding/lsp/lsp_client.go", "TextDocumentDefinition")
// if err != nil {
// 	log.Panicf("Error: %v", err)
// }
// fmt.Println(str)
// fmt.Println(tree_sitter.GetSymbolDefinition("./coding/lsp/lsp_client.go", "ReadWriteCloser"))

//fmt.Println(tree_sitter.GetSymbolDefinition("./frontend/src/components/ChatHistory.vue", "whatever"))
//fmt.Println(tree_sitter.GetSymbolDefinition("./frontend/src/views/HomeView.vue", "whatever"))

//fmt.Println(tree_sitter.GetFileMap("./coding/lsp/lsp_client.go"))
//fmt.Println(tree_sitter.GetFileMap("./coding/lsp/lsp_types.go"))

//parser := sitter.NewParser()
//parser.SetLanguage(vue.GetLanguage())
//sourceCode, _ := os.ReadFile("./frontend/src/views/ChatView.vue")
//tree, _ := parser.ParseCtx(context.Background(), nil, sourceCode)
//embeddedTree, _ := tree_sitter.GetVueEmbeddedTypescriptTree(tree, sourceCode)
//fmt.Println(embeddedTree.RootNode().Content(sourceCode))

//fmt.Println("=====================================")

//fmt.Println(tree_sitter.GetFileMap("./frontend/src/views/ChatView.vue"))

func main() {
	godotenv.Load()
	basePath := "."
	//basePath := "/Users/Shared/sidekick"
	envContainer := env.EnvContainer{
		Env: &env.LocalEnv{
			WorkingDirectory: basePath,
		},
	}
	//_, x, _ := check.CheckFileValidity(envContainer, "dev/user_request.go")
	//fmt.Println(x)

	// ranked dir signature outline
	/*
		ragActivities := persisted_ai.RagActivities{
			DatabaseAccessor: db.NewRedisDatabase(),
			Embedder:         embedding.OpenAIEmbedder{},
		}
		out, err := ragActivities.RankedDirSignatureOutline(persisted_ai.RankedDirSignatureOutlineOptions{
			CharLimit: 12500,
			RankedViaEmbeddingOptions: persisted_ai.RankedViaEmbeddingOptions{
				//WorkspaceId:       "fake",
				WorkspaceId:   "ws_2ifQSfBTLtEkKEd90RbyVf5Zyo8", // django
				EnvContainer:  envContainer,
				EmbeddingType: "oai-te3-sm",
				RankQuery:     "Class methods from nested classes cannot be used as Field.default.\nDescription\n\t \n\t\t(last modified by Mariusz Felisiak)\n\t \nGiven the following model:\n \nclass Profile(models.Model):\n\tclass Capability(models.TextChoices):\n\t\tBASIC = (\"BASIC\", \"Basic\")\n\t\tPROFESSIONAL = (\"PROFESSIONAL\", \"Professional\")\n\t\t\n\t\t@classmethod\n\t\tdef default(cls) -> list[str]:\n\t\t\treturn [cls.BASIC]\n\tcapabilities = ArrayField(\n\t\tmodels.CharField(choices=Capability.choices, max_length=30, blank=True),\n\t\tnull=True,\n\t\tdefault=Capability.default\n\t)\nThe resulting migration contained the following:\n # ...\n\t migrations.AddField(\n\t\t model_name='profile',\n\t\t name='capabilities',\n\t\t field=django.contrib.postgres.fields.ArrayField(base_field=models.CharField(blank=True, choices=[('BASIC', 'Basic'), ('PROFESSIONAL', 'Professional')], max_length=30), default=appname.models.Capability.default, null=True, size=None),\n\t ),\n # ...\nAs you can see, migrations.AddField is passed as argument \"default\" a wrong value \"appname.models.Capability.default\", which leads to an error when trying to migrate. The right value should be \"appname.models.Profile.Capability.default\".\n",
				Secrets: secret_manager.SecretManagerContainer{
					SecretManager: secret_manager.EnvSecretManager{},
				},
			},
		})
		if err != nil {
			panic(err)
		}
		fmt.Println(out)
		fmt.Println(len(out))
	*/

	ca := &coding.CodingActivities{
		LSPActivities: &lsp.LSPActivities{
			LSPClientProvider: func(language string) lsp.LSPClient {
				return &lsp.Jsonrpc2LSPClient{LanguageName: language}
			},
			InitializedClients: map[string]lsp.LSPClient{},
		},
	}

	x, err := ca.BulkGetSymbolDefinitions(coding.DirectorySymDefRequest{
		EnvContainer: envContainer,
		Requests: []coding.FileSymDefRequest{
			{
				FilePath:    "dev/read_file.go",
				SymbolNames: []string{"BulkReadFile"},
			},
			{
				FilePath:    "db/redis_database.go",
				SymbolNames: []string{"PersistTask", "GetTask", "DeleteTask", "AddTaskChange", "GetTaskChanges"},
			},
			{
				FilePath:    "db/database_accessor.go",
				SymbolNames: []string{"DatabaseAccessor"},
			},
			{
				FilePath:    "db/redis_database_test.go",
				SymbolNames: []string{"TestPersistTask", "TestGetTasks", "TestDeleteTask", "TestAddTaskChange"},
			},
		},
		IncludeRelatedSymbols: true,
	})
	if err != nil {
		panic(err)
	}
	fmt.Println(len(x.SymbolDefinitions))

	//fmt.Println(x.Failures)

}
