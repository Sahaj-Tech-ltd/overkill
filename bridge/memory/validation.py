"""One validation contract for memory backends and their direct callers."""
import math
import os

MAX_TOP_K = 1000
MAX_DIMENSION = 16000


def vector_dimension(dimension: int | None = None) -> int:
    value = dimension if dimension is not None else int(os.environ.get("OVERKILL_VECTOR_DIMENSION", "384"))
    if isinstance(value, bool) or not isinstance(value, int) or not 1 <= value <= MAX_DIMENSION:
        raise ValueError(f"Vector dimension must be between 1 and {MAX_DIMENSION}")
    return value


def validate_vector(vector: list[float], dimension: int) -> None:
    if len(vector) != dimension:
        raise ValueError(f"Expected vector dimension {dimension}, got {len(vector)}")
    if any(not math.isfinite(value) or abs(value) > 3.4028234663852886e38 for value in vector):
        raise ValueError("Vectors must contain finite float32 values")
    if not any(value != 0 for value in vector):
        raise ValueError("Vectors must have nonzero magnitude for cosine similarity")


def validate_search(vector: list[float], dimension: int, top_k: int, threshold: float) -> None:
    validate_vector(vector, dimension)
    if isinstance(top_k, bool) or not isinstance(top_k, int) or not 1 <= top_k <= MAX_TOP_K:
        raise ValueError(f"top_k must be between 1 and {MAX_TOP_K}")
    if not math.isfinite(threshold) or not -1 <= threshold <= 1:
        raise ValueError("threshold must be finite and between -1 and 1")
