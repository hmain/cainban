# Testing Documentation

This document outlines testing standards and practices for cainban.

## Testing Philosophy

- **Test behavior, not implementation**: Focus on what the system does, not how it does it
- **Fast feedback**: Tests should run quickly to enable rapid iteration
- **Reliable**: Tests should be deterministic and not flaky
- **Maintainable**: Tests should be easy to understand and modify

## Test Structure

### Unit Tests
- Located alongside source code (e.g., `src/systems/task/task_test.go`)
- Test individual functions and methods in isolation
- Use table-driven tests for multiple scenarios
- Mock external dependencies

### Integration Tests
- Located in `tests/integration/`
- Test system interactions (e.g., storage + task system)
- Use real SQLite database with test data
- Clean up after each test

### End-to-End Tests
- Located in `tests/e2e/`
- Test complete user workflows
- Use temporary directories and databases
- Test CLI commands and MCP server

## Test Naming

- Test functions: `TestFunctionName_Scenario_ExpectedBehavior`
- Test files: `*_test.go`
- Test data: `testdata/` directory

Examples:
```go
func TestCreateTask_ValidInput_ReturnsTask(t *testing.T) {}
func TestCreateTask_EmptyTitle_ReturnsError(t *testing.T) {}
func TestListTasks_EmptyBoard_ReturnsEmptySlice(t *testing.T) {}
```

## Test Data Management

### SQLite Test Database
- Use `:memory:` database for unit tests
- Use temporary files for integration tests
- Always clean up test databases

### Test Fixtures
- Store test data in `testdata/` directories
- Use JSON or YAML for structured test data
- Keep fixtures minimal and focused

## Running Tests

```bash
# All tests
go test ./...

# With coverage
go test -cover ./...

# With race detection
go test -race ./...

# Specific package
go test ./src/systems/task/

# Verbose output
go test -v ./...

# Memory profiling
go test -memprofile=mem.prof ./...
```

## Test Quality Standards

### Coverage
- Aim for >80% code coverage
- Focus on critical paths and error handling
- Don't chase 100% coverage at the expense of test quality

### Performance
- Unit tests should complete in <100ms
- Integration tests should complete in <1s
- Use `testing.Short()` for long-running tests

### Error Testing
- Test all error conditions
- Verify error messages are helpful
- Test error propagation through system boundaries

## Mocking Guidelines

### When to Mock
- External services (databases, APIs)
- File system operations
- Time-dependent operations
- Network operations

### When NOT to Mock
- Simple data structures
- Pure functions
- Internal system interactions (prefer integration tests)

### Mock Implementation
- Use interfaces for mockable dependencies
- Keep mocks simple and focused
- Verify mock interactions when behavior matters

## Test Examples

### Table-Driven Test
```go
func TestValidateTaskTitle(t *testing.T) {
    tests := []struct {
        name    string
        title   string
        wantErr bool
    }{
        {"valid title", "Fix bug in parser", false},
        {"empty title", "", true},
        {"whitespace only", "   ", true},
        {"too long", strings.Repeat("a", 256), true},
    }
    
    for _, tt := range tests {
        t.Run(tt.name, func(t *testing.T) {
            err := ValidateTaskTitle(tt.title)
            if (err != nil) != tt.wantErr {
                t.Errorf("ValidateTaskTitle() error = %v, wantErr %v", err, tt.wantErr)
            }
        })
    }
}
```

### Integration Test
```go
func TestTaskSystem_CreateAndRetrieve(t *testing.T) {
    // Setup
    db := setupTestDB(t)
    defer db.Close()
    
    taskSys := task.New(db)
    
    // Test
    created, err := taskSys.Create("Test task", "Description")
    require.NoError(t, err)
    
    retrieved, err := taskSys.GetByID(created.ID)
    require.NoError(t, err)
    
    // Verify
    assert.Equal(t, created.Title, retrieved.Title)
    assert.Equal(t, created.Description, retrieved.Description)
}
```

## Continuous Integration

### Pre-commit Checks
- `go vet ./...` - Static analysis
- `go test -race ./...` - Race condition detection
- `golangci-lint run` - Linting
- `go mod tidy` - Dependency management

### Test Environment
- Run tests in clean environment
- Use consistent Go version
- Test on multiple platforms if needed

## Test Documentation

- Document complex test scenarios
- Explain test data setup when non-obvious
- Include examples of expected behavior
- Document known limitations or edge cases
