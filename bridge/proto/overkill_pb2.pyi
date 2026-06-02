from google.protobuf.internal import containers as _containers
from google.protobuf import descriptor as _descriptor
from google.protobuf import message as _message
from typing import ClassVar as _ClassVar, Iterable as _Iterable, Mapping as _Mapping, Optional as _Optional, Union as _Union

DESCRIPTOR: _descriptor.FileDescriptor

class CompactRequest(_message.Message):
    __slots__ = ["content", "model", "style", "target_tokens"]
    CONTENT_FIELD_NUMBER: _ClassVar[int]
    MODEL_FIELD_NUMBER: _ClassVar[int]
    STYLE_FIELD_NUMBER: _ClassVar[int]
    TARGET_TOKENS_FIELD_NUMBER: _ClassVar[int]
    content: str
    model: str
    style: str
    target_tokens: int
    def __init__(self, content: _Optional[str] = ..., model: _Optional[str] = ..., target_tokens: _Optional[int] = ..., style: _Optional[str] = ...) -> None: ...

class CompactResponse(_message.Message):
    __slots__ = ["original_tokens", "success", "summary", "summary_tokens"]
    ORIGINAL_TOKENS_FIELD_NUMBER: _ClassVar[int]
    SUCCESS_FIELD_NUMBER: _ClassVar[int]
    SUMMARY_FIELD_NUMBER: _ClassVar[int]
    SUMMARY_TOKENS_FIELD_NUMBER: _ClassVar[int]
    original_tokens: int
    success: bool
    summary: str
    summary_tokens: int
    def __init__(self, summary: _Optional[str] = ..., original_tokens: _Optional[int] = ..., summary_tokens: _Optional[int] = ..., success: bool = ...) -> None: ...

class DeleteVectorRequest(_message.Message):
    __slots__ = ["backend", "id"]
    BACKEND_FIELD_NUMBER: _ClassVar[int]
    ID_FIELD_NUMBER: _ClassVar[int]
    backend: str
    id: str
    def __init__(self, id: _Optional[str] = ..., backend: _Optional[str] = ...) -> None: ...

class DeleteVectorResponse(_message.Message):
    __slots__ = ["success"]
    SUCCESS_FIELD_NUMBER: _ClassVar[int]
    success: bool
    def __init__(self, success: bool = ...) -> None: ...

class EmbedBatchRequest(_message.Message):
    __slots__ = ["model", "texts"]
    MODEL_FIELD_NUMBER: _ClassVar[int]
    TEXTS_FIELD_NUMBER: _ClassVar[int]
    model: str
    texts: _containers.RepeatedScalarFieldContainer[str]
    def __init__(self, texts: _Optional[_Iterable[str]] = ..., model: _Optional[str] = ...) -> None: ...

class EmbedBatchResponse(_message.Message):
    __slots__ = ["results"]
    RESULTS_FIELD_NUMBER: _ClassVar[int]
    results: _containers.RepeatedCompositeFieldContainer[EmbedResult]
    def __init__(self, results: _Optional[_Iterable[_Union[EmbedResult, _Mapping]]] = ...) -> None: ...

class EmbedRequest(_message.Message):
    __slots__ = ["model", "text"]
    MODEL_FIELD_NUMBER: _ClassVar[int]
    TEXT_FIELD_NUMBER: _ClassVar[int]
    model: str
    text: str
    def __init__(self, text: _Optional[str] = ..., model: _Optional[str] = ...) -> None: ...

class EmbedResponse(_message.Message):
    __slots__ = ["embedding", "tokens"]
    EMBEDDING_FIELD_NUMBER: _ClassVar[int]
    TOKENS_FIELD_NUMBER: _ClassVar[int]
    embedding: _containers.RepeatedScalarFieldContainer[float]
    tokens: int
    def __init__(self, embedding: _Optional[_Iterable[float]] = ..., tokens: _Optional[int] = ...) -> None: ...

class EmbedResult(_message.Message):
    __slots__ = ["embedding", "tokens"]
    EMBEDDING_FIELD_NUMBER: _ClassVar[int]
    TOKENS_FIELD_NUMBER: _ClassVar[int]
    embedding: _containers.RepeatedScalarFieldContainer[float]
    tokens: int
    def __init__(self, embedding: _Optional[_Iterable[float]] = ..., tokens: _Optional[int] = ...) -> None: ...

class PingRequest(_message.Message):
    __slots__ = []
    def __init__(self) -> None: ...

class PongResponse(_message.Message):
    __slots__ = ["status", "version"]
    STATUS_FIELD_NUMBER: _ClassVar[int]
    VERSION_FIELD_NUMBER: _ClassVar[int]
    status: str
    version: str
    def __init__(self, status: _Optional[str] = ..., version: _Optional[str] = ...) -> None: ...

class RerankRequest(_message.Message):
    __slots__ = ["documents", "model", "query", "top_n"]
    DOCUMENTS_FIELD_NUMBER: _ClassVar[int]
    MODEL_FIELD_NUMBER: _ClassVar[int]
    QUERY_FIELD_NUMBER: _ClassVar[int]
    TOP_N_FIELD_NUMBER: _ClassVar[int]
    documents: _containers.RepeatedScalarFieldContainer[str]
    model: str
    query: str
    top_n: int
    def __init__(self, query: _Optional[str] = ..., documents: _Optional[_Iterable[str]] = ..., model: _Optional[str] = ..., top_n: _Optional[int] = ...) -> None: ...

class RerankResponse(_message.Message):
    __slots__ = ["results"]
    RESULTS_FIELD_NUMBER: _ClassVar[int]
    results: _containers.RepeatedCompositeFieldContainer[RerankResult]
    def __init__(self, results: _Optional[_Iterable[_Union[RerankResult, _Mapping]]] = ...) -> None: ...

class RerankResult(_message.Message):
    __slots__ = ["index", "relevance_score", "text"]
    INDEX_FIELD_NUMBER: _ClassVar[int]
    RELEVANCE_SCORE_FIELD_NUMBER: _ClassVar[int]
    TEXT_FIELD_NUMBER: _ClassVar[int]
    index: int
    relevance_score: float
    text: str
    def __init__(self, index: _Optional[int] = ..., relevance_score: _Optional[float] = ..., text: _Optional[str] = ...) -> None: ...

class SearchResult(_message.Message):
    __slots__ = ["content", "id", "metadata", "score"]
    class MetadataEntry(_message.Message):
        __slots__ = ["key", "value"]
        KEY_FIELD_NUMBER: _ClassVar[int]
        VALUE_FIELD_NUMBER: _ClassVar[int]
        key: str
        value: str
        def __init__(self, key: _Optional[str] = ..., value: _Optional[str] = ...) -> None: ...
    CONTENT_FIELD_NUMBER: _ClassVar[int]
    ID_FIELD_NUMBER: _ClassVar[int]
    METADATA_FIELD_NUMBER: _ClassVar[int]
    SCORE_FIELD_NUMBER: _ClassVar[int]
    content: str
    id: str
    metadata: _containers.ScalarMap[str, str]
    score: float
    def __init__(self, id: _Optional[str] = ..., score: _Optional[float] = ..., content: _Optional[str] = ..., metadata: _Optional[_Mapping[str, str]] = ...) -> None: ...

class SearchVectorsRequest(_message.Message):
    __slots__ = ["backend", "filters", "query", "threshold", "top_k"]
    class FiltersEntry(_message.Message):
        __slots__ = ["key", "value"]
        KEY_FIELD_NUMBER: _ClassVar[int]
        VALUE_FIELD_NUMBER: _ClassVar[int]
        key: str
        value: str
        def __init__(self, key: _Optional[str] = ..., value: _Optional[str] = ...) -> None: ...
    BACKEND_FIELD_NUMBER: _ClassVar[int]
    FILTERS_FIELD_NUMBER: _ClassVar[int]
    QUERY_FIELD_NUMBER: _ClassVar[int]
    THRESHOLD_FIELD_NUMBER: _ClassVar[int]
    TOP_K_FIELD_NUMBER: _ClassVar[int]
    backend: str
    filters: _containers.ScalarMap[str, str]
    query: _containers.RepeatedScalarFieldContainer[float]
    threshold: float
    top_k: int
    def __init__(self, query: _Optional[_Iterable[float]] = ..., top_k: _Optional[int] = ..., threshold: _Optional[float] = ..., backend: _Optional[str] = ..., filters: _Optional[_Mapping[str, str]] = ...) -> None: ...

class SearchVectorsResponse(_message.Message):
    __slots__ = ["results"]
    RESULTS_FIELD_NUMBER: _ClassVar[int]
    results: _containers.RepeatedCompositeFieldContainer[SearchResult]
    def __init__(self, results: _Optional[_Iterable[_Union[SearchResult, _Mapping]]] = ...) -> None: ...

class StoreVectorRequest(_message.Message):
    __slots__ = ["backend", "entry"]
    BACKEND_FIELD_NUMBER: _ClassVar[int]
    ENTRY_FIELD_NUMBER: _ClassVar[int]
    backend: str
    entry: VectorEntry
    def __init__(self, entry: _Optional[_Union[VectorEntry, _Mapping]] = ..., backend: _Optional[str] = ...) -> None: ...

class StoreVectorResponse(_message.Message):
    __slots__ = ["id", "success"]
    ID_FIELD_NUMBER: _ClassVar[int]
    SUCCESS_FIELD_NUMBER: _ClassVar[int]
    id: str
    success: bool
    def __init__(self, id: _Optional[str] = ..., success: bool = ...) -> None: ...

class VectorEntry(_message.Message):
    __slots__ = ["content", "embedding", "id", "metadata"]
    class MetadataEntry(_message.Message):
        __slots__ = ["key", "value"]
        KEY_FIELD_NUMBER: _ClassVar[int]
        VALUE_FIELD_NUMBER: _ClassVar[int]
        key: str
        value: str
        def __init__(self, key: _Optional[str] = ..., value: _Optional[str] = ...) -> None: ...
    CONTENT_FIELD_NUMBER: _ClassVar[int]
    EMBEDDING_FIELD_NUMBER: _ClassVar[int]
    ID_FIELD_NUMBER: _ClassVar[int]
    METADATA_FIELD_NUMBER: _ClassVar[int]
    content: str
    embedding: _containers.RepeatedScalarFieldContainer[float]
    id: str
    metadata: _containers.ScalarMap[str, str]
    def __init__(self, id: _Optional[str] = ..., embedding: _Optional[_Iterable[float]] = ..., content: _Optional[str] = ..., metadata: _Optional[_Mapping[str, str]] = ...) -> None: ...
