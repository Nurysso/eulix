from abc import ABC, abstractmethod
from typing import List, Dict, Optional
import numpy as np
from core.types import Chunk

class EmbeddingGeneratorBase(ABC):
    """Abstract interface for embedding generators."""

    @property
    @abstractmethod
    def dimension(self) -> int:
        """Return the embedding dimension."""
        pass

    @property
    @abstractmethod
    def model_name(self) -> str:
        """Return the model identifier."""
        pass

    @abstractmethod
    def _embed_batch(self, texts: List[str], fixed_len: Optional[int] = None) -> np.ndarray:
        """Embed a batch of texts. Internal method."""
        pass

    @abstractmethod
    def generate_vectors(self, chunks: List[Chunk]) -> Dict[str, np.ndarray]:
        """Generate embeddings for all chunks."""
        pass

    @abstractmethod
    def embed_query(self, query: str) -> np.ndarray:
        """Embed a single query string."""
        pass
