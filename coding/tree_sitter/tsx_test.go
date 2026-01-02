package tree_sitter

import (
	"os"
	"testing"

	"sidekick/utils"

	"github.com/stretchr/testify/assert"
)

func TestGetFileSignaturesStringTsx(t *testing.T) {
	t.Parallel()
	testCases := []struct {
		name     string
		code     string
		expected string
	}{
		{
			name:     "empty",
			code:     "",
			expected: "",
		},
		{
			name: "functional component with props",
			code: `interface ButtonProps {
  onClick: () => void;
  children: React.ReactNode;
}

const Button: React.FC<ButtonProps> = ({ onClick, children }) => {
  return <button onClick={onClick}>{children}</button>;
};`,
			expected: `interface ButtonProps {
  onClick: () => void;
  children: React.ReactNode;
}
---
const Button: React.FC<ButtonProps> = ({ onClick, children }) => {
  return <button onClick={onClick}>{children}</button>;
};
---
`,
		},
		{
			name: "class component with state",
			code: `class Counter extends React.Component<{}, { count: number }> {
  state = { count: 0 };

  increment = () => {
    this.setState(prev => ({ count: prev.count + 1 }));
  };

  render() {
    return (
      <div>Count: {this.state.count}</div>
    );
  }
}`,
			expected: `class Counter extends React.Component<{}, { count: number }>
	state
	increment
	render()
---
`,
		},
		{
			name: "custom hook",
			code: `function useCounter(initial: number) {
  const [count, setCount] = React.useState(initial);
  const increment = () => setCount(c => c + 1);
  return { count, increment };
}`,
			expected: `function useCounter(initial: number)
---
`,
		},
		{
			name: "higher order component",
			code: `function withLogging<P extends object>(WrappedComponent: React.ComponentType<P>) {
  return class WithLogging extends React.Component<P> {
    componentDidMount() {
      console.log('Component mounted');
    }

    render() {
      return <WrappedComponent {...this.props} />;
    }
  };
}`,
			expected: `function withLogging<P extends object>(WrappedComponent: React.ComponentType<P>)
---
	class WithLogging extends React.Component<P>
		componentDidMount()
		render()
---
`,
		},
		{
			name: "complex TSX",
			code: `import { useState, useEffect } from "react";
interface User {
  id: number;
  name: string;
  email: string;
  username: string;
}

interface UsersState {
  users: User[] | null;
  loading: boolean;
  error: string | null;
}

const useUsers = (): UsersState => {
  const [state, setState] = useState<UsersState>({users: null, loading: true, error: null});
  useEffect(() => {
    async function fetchUsers() {
      const response = await fetch('/api/users');
      const data = await response.json();
      setState({users: data, loading: false, error: null});
    }
    fetchUsers();
  }, []);
  return state;
};

function UserList() {
  const { users, loading, error } = useUsers();
  return (
    <div>
      {loading && <p>Loading...</p>}
      {error && <p>Error: {error}</p>}
      {users && users.map((user: User) => (
        <div key={user.id}>
          <h2>{user.name}</h2>
          <p>{user.email}</p>
        </div>
      ))}
    </div>
  );
}

export default UserList;`,
			expected: `interface User {
  id: number;
  name: string;
  email: string;
  username: string;
}
---
interface UsersState {
  users: User[] | null;
  loading: boolean;
  error: string | null;
}
---
const useUsers = (): UsersState => {
  const [state, setState] = useState<UsersState>({users: null, loading: true, error: null});
  useEffect(() => {
  [...]
---
async function fetchUsers()
---
function UserList()
---
`,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			// Create a temporary file with the test case code
			tmpfile, err := os.CreateTemp("", "test*.tsx")
			if err != nil {
				t.Fatalf("Failed to create temp file: %v", err)
			}
			defer os.Remove(tmpfile.Name())

			if _, err := tmpfile.Write([]byte(tc.code)); err != nil {
				t.Fatalf("Failed to write to temp file: %v", err)
			}
			if err := tmpfile.Close(); err != nil {
				t.Fatalf("Failed to close temp file: %v", err)
			}

			// Call GetFileSignatures with the path to the temp file
			result, err := GetFileSignaturesString(tmpfile.Name())
			if err != nil {
				t.Fatalf("GetFileSignatures returned an error: %v", err)
			}

			// Check the result
			if result != tc.expected {
				t.Errorf("GetFileSignatures returned incorrect result. Expected:\n%s\nGot:\n%s", utils.PanicJSON(tc.expected), utils.PanicJSON(result))
			}
		})
	}
}

func TestGetFileSymbolsStringTsx(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		code     string
		expected string
	}{
		{
			name:     "empty",
			code:     "",
			expected: "",
		},
		{
			name: "functional component",
			code: `const Button: React.FC<ButtonProps> = ({ onClick, children }) => {
  return <button onClick={onClick}>{children}</button>;
};`,
			expected: "Button",
		},
		{
			name: "class component",
			code: `class Counter extends React.Component<{}, { count: number }> {
  render() {
    return <div>Count: {this.state.count}</div>;
  }
}`,
			expected: "Counter, render",
		},
		{
			name: "custom hook",
			code: `function useCounter(initial: number) {
  return { count: initial };
}`,
			expected: "useCounter",
		},
		{
			name: "interface and component",
			code: `interface ButtonProps {
  onClick: () => void;
}

const Button: React.FC<ButtonProps> = ({ onClick }) => {
  return <button onClick={onClick} />;
};`,
			expected: "ButtonProps, Button",
		},
		{
			name: "higher order component",
			code: `function withLogging<P extends object>(WrappedComponent: React.ComponentType<P>) {
  return class WithLogging extends React.Component<P> {
    render() {
      return <WrappedComponent {...this.props} />;
    }
  };
}`,
			expected: "withLogging, WithLogging, render",
		},
		{
			name: "complex TSX",
			code: `import { useState, useEffect } from "react";
interface User {
  id: number;
  name: string;
  email: string;
  username: string;
}

interface UsersState {
  users: User[] | null;
  loading: boolean;
  error: string | null;
}

const useUsers = (): UsersState => {
  const [state, setState] = useState<UsersState>({users: null, loading: true, error: null});
  useEffect(() => {
    async function fetchUsers() {
      const response = await fetch('/api/users');
      const data = await response.json();
      setState({users: data, loading: false, error: null});
    }
    fetchUsers();
  }, []);
  return state;
};

function UserList() {
  const { users, loading, error } = useUsers();
  return (
    <div>
      {loading && <p>Loading...</p>}
      {error && <p>Error: {error}</p>}
      {users && users.map((user: User) => (
        <div key={user.id}>
          <h2>{user.name}</h2>
          <p>{user.email}</p>
        </div>
      ))}
    </div>
  );
}

export default UserList;`,
			expected: "User, UsersState, useUsers, fetchUsers, UserList",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			tmpfile, err := os.CreateTemp("", "*.tsx")
			if err != nil {
				t.Fatalf("Failed to create temp file: %v", err)
			}
			defer os.Remove(tmpfile.Name())

			if _, err := tmpfile.Write([]byte(test.code)); err != nil {
				t.Fatalf("Failed to write to temp file: %v", err)
			}
			if err := tmpfile.Close(); err != nil {
				t.Fatalf("Failed to close temp file: %v", err)
			}

			symbolsString, err := GetFileSymbolsString(tmpfile.Name())
			if err != nil {
				t.Fatalf("Failed to get symbols: %v", err)
			}

			if symbolsString != test.expected {
				t.Errorf("Got %s, expected %s", symbolsString, test.expected)
			}
		})
	}
}

func TestGetFileHeadersStringTsx(t *testing.T) {
	t.Parallel()
	testCases := []struct {
		name     string
		code     string
		expected string
	}{
		{
			name:     "empty",
			code:     "",
			expected: "",
		},
		{
			name:     "no imports",
			code:     "const Button = () => <button />;",
			expected: "",
		},
		{
			name:     "react import",
			code:     "import React from 'react';",
			expected: "import React from 'react';\n",
		},
		{
			name:     "multiple imports",
			code:     "import React from 'react';\nimport { useState } from 'react';\nimport styled from 'styled-components';",
			expected: "import React from 'react';\nimport { useState } from 'react';\nimport styled from 'styled-components';\n",
		},
		{
			name:     "import with type",
			code:     "import type { ReactNode } from 'react';",
			expected: "import type { ReactNode } from 'react';\n",
		},
		{
			name:     "import with alias",
			code:     "import { Component as ReactComponent } from 'react';",
			expected: "import { Component as ReactComponent } from 'react';\n",
		},
		{
			name:     "import with namespace",
			code:     "import * as React from 'react';",
			expected: "import * as React from 'react';\n",
		},
		{
			name:     "nested imports",
			code:     "function x() {\n    import React from 'react';\n}",
			expected: "",
		},
		{
			name:     "import aliases",
			code:     "import { foo as f, bar as b } from 'bar';",
			expected: "import { foo as f, bar as b } from 'bar';\n",
		},
		{
			name:     "import equals",
			code:     "import foo = bar;",
			expected: "import foo = bar;\n",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			// Create a temporary file with the test case code
			tmpfile, err := os.CreateTemp("", "test*.tsx")
			if err != nil {
				t.Fatalf("Failed to create temp file: %v", err)
			}
			defer os.Remove(tmpfile.Name())
			if _, err := tmpfile.Write([]byte(tc.code)); err != nil {
				t.Fatalf("Failed to write to temp file: %v", err)
			}
			if err := tmpfile.Close(); err != nil {
				t.Fatalf("Failed to close temp file: %v", err)
			}
			result, err := GetFileHeadersString(tmpfile.Name(), 0)
			assert.Nil(t, err)
			// Check the result
			if result != tc.expected {
				t.Errorf("GetFileHeadersString returned incorrect result. Expected:\n%s\nGot:\n%s", utils.PanicJSON(tc.expected), utils.PanicJSON(result))
			}
		})
	}
}

func TestNormalizeSymbolFromSnippet_TSX(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		snippet  string
		expected string
	}{
		{
			name:     "Top-level function",
			snippet:  "function comp(): JSX.Element { return <div/> }",
			expected: "comp",
		},
		{
			name:     "Class with method returns class symbol as first",
			snippet:  "class SomeComponent { render(): JSX.Element { return <span/> } }",
			expected: "SomeComponent",
		},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := NormalizeSymbolFromSnippet("tsx", tc.snippet)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tc.expected {
				t.Fatalf("expected %q, got %q", tc.expected, got)
			}
		})
	}
}
