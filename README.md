# WIP: Mutation tests for GO

Implementation of a multistage mutation testing architecture, that works as Follows:

### 1. Code Instrumentation:
Each statement recieves an instruction that will determine wether it was reached or not, much like test coverage.
### 2. Normal Test Execution:
The unit test suite is executed with go test, generating metadata of code reachability.

### 3. Reachability Calculation:
The metadata is used to form a map from Test -> Block of Code. So we know exactly which blocks of code a test can reach. (This could possibly be generated for us via go test with coverage data, but not for now).

### 4. Mutation Generation:
Each reachable block of code is then fed to the mutators which generate the enabled types of mutations in them.

### 5. Mutation Writing:
!! Experimental (Still not tested)

The idea is to avoid one compile per mutation by writing them at once, in the form of functions as follows:
```go
// Original Code:
a := a + 1

// Written Code:
_Mutation("somefile:1", func () {a := a + 1}, func() {a := a - 1})
```

Where the `_Mutation` function is define as follows:
```go
func _Mutation(id string, origStmt func(), mutStmt func()) {
  if env, ok := os.LookupEnv("EnabledMutation"); env == id {
		mutStmt()
	} else {
		origStmt()
	}
}
```
### 6. Execution:
The execution is made by repeatedly invoking `go test -run='reachableTest'` with the EnabledMutation global state set to the id of the mutation.

In theory, the complile cache can be reused because all the mutation happens at runtime. If this theory fails this will be implemented as a normal 1 compile per mutation for now.
