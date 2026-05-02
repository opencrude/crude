#!/usr/bin/env python3
"""Fibonacci number calculator."""


def fibonacci(n: int) -> int:
    """Return the nth Fibonacci number."""
    if n < 0:
        raise ValueError("n must be non-negative")
    if n == 0:
        return 0
    if n == 1:
        return 1
    
    a, b = 0, 1
    for _ in range(2, n + 1):
        a, b = b, a + b
    return b


def fibonacci_sequence(count: int) -> list[int]:
    """Return a list of the first `count` Fibonacci numbers."""
    if count < 0:
        raise ValueError("count must be non-negative")
    return [fibonacci(i) for i in range(count)]


if __name__ == "__main__":
    import sys

    if len(sys.argv) > 1:
        try:
            n = int(sys.argv[1])
            print(f"Fibonacci({n}) = {fibonacci(n)}")
        except ValueError as e:
            print(f"Error: {e}")
            sys.exit(1)
    else:
        # Print first 20 Fibonacci numbers by default
        print("First 20 Fibonacci numbers:")
        print(fibonacci_sequence(20))