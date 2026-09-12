"""Nearest-rank percentiles for PGO cold-start evidence."""

from __future__ import annotations

import math
import sys


def nearest_rank_index(count: int, percentile: float) -> int:
    if count < 1:
        raise ValueError("sample set is empty")
    index = math.ceil(percentile * count) - 1
    if index < 0:
        return 0
    if index >= count:
        return count - 1
    return index


def p95_ns(samples: list[int]) -> int:
    if not samples:
        raise ValueError("sample set is empty")
    ordered = sorted(samples)
    return int(ordered[nearest_rank_index(len(ordered), 0.95)])


def main() -> int:
    cases = [
        ([1], 1),
        ([2, 1], 2),
        ([1, 2, 3], 3),
        ([10, 20, 30, 40, 50, 60, 70], 70),
        ([7, 7, 7, 7, 7, 7, 7], 7),
        ([70, 10, 30, 20, 60, 40, 50], 70),
        (list(range(1, 21)), 19),
    ]
    for samples, want in cases:
        got = p95_ns(samples)
        if got != want:
            print(f"p95_ns({samples}) = {got}, want {want}", file=sys.stderr)
            return 1
    try:
        p95_ns([])
    except ValueError:
        return 0
    print("empty sample set was accepted", file=sys.stderr)
    return 1


if __name__ == "__main__":
    raise SystemExit(main())
