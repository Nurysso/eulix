# run-failed-tests.ps1  (run from your repo root, where go.mod is)
$failedTests = @(
    "TestFileIsPrivate","TestEnvFile","TestE2ESetRepoPathRejectsBadPaths",
    "TestE2EUsageQueryReadsOnlyInsideTheRepo","TestE2ERootsBindTheFirstInitializedRoot",
    "TestRootsNeverOverrideAnExplicitChoice","TestE2EBindingDoesNotLockChatHistory",
    "TestURIPath","TestProductionRouterLoadsKBOnDemand","TestProductionRouterLoadsOwnDependencies",
    "TestFuzzySearch","TestResolveCallGraphEntity","TestHandleCallGraph","TestHandleLocation",
    "TestHandleUsage","TestHandleDependency","TestHandleMetrics","TestHandleEntryPoints",
    "TestHandleFileStructure","TestHandleTodosQuery","TestHandleCodeGeneration",
    "TestRouterPlumbing","TestQueryEngine_Routing","TestPromptOrAnswer_Routing",
    "TestTaskBodies_AllWellFormed","TestGetTaskBody","TestBM25TieBreakIsDeterministic",
    "TestEhnasSearchRanksLongUnnamedChunkLower","TestPRFTokensExistInIndex"
)

$outputFile = "failed_tests_output.txt"
$pattern = "^(" + ($failedTests -join "|") + ")(/|$)"

Write-Host "Running $($failedTests.Count) tests..." -ForegroundColor Cyan

go test -v -count=1 ./... -run $pattern 2>&1 |
    ForEach-Object { "$_" } |
    Out-File -FilePath $outputFile -Encoding utf8

Write-Host "Done! Output saved to $outputFile" -ForegroundColor Green \ No newline at end of file
