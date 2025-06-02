package main

import (
	"fmt"
	"log"
	"sidekick"
	"sidekick/common"
	"sidekick/env"
	"sidekick/persisted_ai"
	"sidekick/secret_manager"

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
	basePath := "/Users/Shared/sidekick"
	//basePath := "/Users/Shared/intellij-sidekick/src/main/kotlin/com/github/sidedev/sidekick/api/response"

	envContainer := env.EnvContainer{
		Env: &env.LocalEnv{
			WorkingDirectory: basePath,
		},
	}
	//_, x, _ := check.CheckFileValidity(envContainer, "dev/user_request.go")
	//fmt.Println(x)

	// ranked dir signature outline

	service, err := sidekick.GetService()
	if err != nil {
		log.Fatal(err)
	}
	ragActivities := persisted_ai.RagActivities{
		DatabaseAccessor: service,
	}
	out, err := ragActivities.RankedDirSignatureOutline(persisted_ai.RankedDirSignatureOutlineOptions{
		CharLimit: 12500,
		RankedViaEmbeddingOptions: persisted_ai.RankedViaEmbeddingOptions{
			WorkspaceId: "fake",
			//WorkspaceId:   "ws_2ifQSfBTLtEkKEd90RbyVf5Zyo8", // django
			EnvContainer: envContainer,
			ModelConfig: common.ModelConfig{
				Provider: "google",
				//Model:    string(openai.SmallEmbedding3),
			},
			RankQuery: "i'm interested in embeddings",
			Secrets: secret_manager.SecretManagerContainer{
				SecretManager: secret_manager.KeyringSecretManager{},
			},
		},
	})
	if err != nil {
		panic(err)
	}
	fmt.Println(out)
	fmt.Println(len(out))

	///////
	//    ca := &coding.CodingActivities{
	//    	LSPActivities: &lsp.LSPActivities{
	//    		LSPClientProvider: func(language string) lsp.LSPClient {
	//    			return &lsp.Jsonrpc2LSPClient{LanguageName: language}
	//    		},
	//    		InitializedClients: map[string]lsp.LSPClient{},
	//    	},
	//    }

	//    x, err := ca.BulkGetSymbolDefinitions(coding.DirectorySymDefRequest{
	//    	EnvContainer: envContainer,
	//    	Requests: []coding.FileSymDefRequest{
	//    		{
	//    			FilePath:    "src/main/kotlin/com/github/sidedev/sidekick/api/Task.kt",
	//    			SymbolNames: []string{"Task"},
	//    		},
	//    		/*
	//    		{
	//    			FilePath: "build.gradle.kts",
	//    		},
	//    		*/
	//    		//{
	//    		//	FilePath:    "src/main/kotlin/com/github/sidedev/sidekick/api/SidekickService.kt",
	//    		//	SymbolNames: []string{"client"},
	//    		//},

	//    		/*
	//    			{
	//    				FilePath:    "dev/read_file.go",
	//    				SymbolNames: []string{"BulkReadFile"},
	//    			},
	//    			{
	//    				FilePath:    "coding/tree_sitter/symbol_outline.go",
	//    				SymbolNames: []string{"getSitterLanguage", "normalizeLanguageName"},
	//    			},
	//    		*/

	//    		/*
	//    			{
	//    				FilePath:    "db/redis_database.go",
	//    				SymbolNames: []string{"PersistTask", "GetTask", "DeleteTask", "AddTaskChange", "GetTaskChanges"},
	//    			},
	//    			{
	//    				FilePath:    "db/database_accessor.go",
	//    				SymbolNames: []string{"DatabaseAccessor"},
	//    			},
	//    			{
	//    				FilePath:    "db/redis_database_test.go",
	//    				SymbolNames: []string{"TestPersistTask", "TestGetTasks", "TestDeleteTask", "TestAddTaskChange"},
	//    			},
	//    		*/
	//    	},
	//    	IncludeRelatedSymbols: true,
	//    })
	//    if err != nil {
	//    	panic(err)
	//    }
	//    fmt.Println(x.SymbolDefinitions)

	//fmt.Println(x.Failures)

}
