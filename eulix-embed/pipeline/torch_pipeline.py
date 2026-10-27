# Copyright (C) 2026 Dawood K.
# SPDX-License-Identifier: Apache-2.0

# Maintainer Dawood (Nurysso) contact - nurysso [at] proton.me
# Pipeline (Torch)

# PyTorch engine for the embedding pipeline. Shared logic lives in base.py;
# this file only provides the Torch generator and tqdm loading.

from embedders.torch_embed import EmbeddingGeneratorTorch
from pipeline.base_pipeline import BaseEmbeddingPipeline


class EmbeddingPipelineTorch(BaseEmbeddingPipeline[EmbeddingGeneratorTorch]):
    ENGINE_LABEL = "PyTorch"
    ENGINE_SHORT = "Torch"

    def _make_generator(self, model_name: str, device: str | None, batch_size: int | None) -> EmbeddingGeneratorTorch:
        return EmbeddingGeneratorTorch(model_name=model_name, device=device, batch_size=batch_size)
