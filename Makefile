format:
	@find . -print | grep --regex '.*\.go' | xargs goimports -w -local "github.com/studioTeaTwo/simple-l402-server"
verify:
	@staticcheck ./... && go vet ./...