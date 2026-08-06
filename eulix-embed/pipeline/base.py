from abc import ABC, abstractmethod
from pathlib import Path
import argparse
from typing import Optional
from typing import List
from core.types import Chunk

class EmbeddingPipelineBase(ABC):
    """Abstract interface for embedding pipelines."""

    def __init__(
        self,
        model_name: str,
        max_chunk_size: int,
        device: Optional[str],
        batch_size: Optional[int],
        save_json: bool,
        quantize: bool,
        debug: bool
    ):
        self.model_name = model_name
        self.max_chunk_size = max_chunk_size
        self.device = device
        self.batch_size = batch_size
        self.save_json = save_json
        self.quantize = quantize
        self.debug = debug

    @abstractmethod
    def _process_step3_step4(self, chunks: List[Chunk], output_dir: Path) -> int:
        """Embed chunks and write binaries. Returns dimension."""
        pass

    @abstractmethod
    def process(self, kb_path: Path, output_dir: Path, args: argparse.Namespace) -> None:
        """Full pipeline execution."""
        pass
