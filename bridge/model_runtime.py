"""Shared, explicit semantic model availability contract."""
import os


class ModelUnavailableError(RuntimeError):
    """A semantic capability could not load its requested model."""


def mock_models_enabled() -> bool:
    return os.environ.get("OVERKILL_ALLOW_MOCK_MODELS", "") == "1"
