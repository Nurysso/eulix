# Copyright (C) 2026 Dawood K.
# SPDX-License-Identifier: Apache-2.0

# Maintainer Dawood (Nurysso) contact - nurysso [at] proton.me
# Pipeline (ONNX)

# ONNX Runtime engine for the embedding pipeline. Shared logic lives in base.py;
# this file only provides the ONNX generator and per-bucket batch sizing.
# This is also the default engine of eulix_embed

from embedders.onnx_embed import EmbeddingGeneratorOnnx
from pipeline.base_pipeline import BaseEmbeddingPipeline


class EmbeddingPipelineOnnx(BaseEmbeddingPipeline[EmbeddingGeneratorOnnx]):
    ENGINE_LABEL = "ONNX Runtime"
    ENGINE_SHORT = "ONNX"
    CHECK_DISK = False

    def _make_generator(self, model_name: str, device: str | None, batch_size: int | None) -> EmbeddingGeneratorOnnx:
        return EmbeddingGeneratorOnnx(model_name=model_name, device=device, batch_size=batch_size)

    def _bacth_size_for(self, gen: EmbeddingGeneratorOnnx, blen: int) -> int:
        return gen.batch_size_for(blen)
