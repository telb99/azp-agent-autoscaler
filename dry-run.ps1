# go run main.go -action dry-run -log-level info -name "azp-agent-early-adopter-pr" -namespace "cluster" -token "$Env:NUGET_PERSONAL_ACCESS_TOKEN" -url "https://dev.azure.com/$env:org"
go run main.go -action dry-run -log-level trace -name "azp-agent-sandbox" -namespace "cluster" -token "$Env:NUGET_PERSONAL_ACCESS_TOKEN" -url "https://dev.azure.com/$env:org"
