# Copyright (C) 2026 Dawood K.
# SPDX-License-Identifier: Apache-2.0

import contextlib
import io
import sys
import unittest
from unittest.mock import MagicMock, patch

import numpy

from embedders.onnx_embed import EmbeddingGeneratorOnnx
from utils.req import require_ml


class CPUAndBannerTests(unittest.TestCase):
    def test_cpu_torch_does_not_initialize_cuda(self):
        torch = MagicMock()
        torch.cuda.is_available.return_value = False
        torch.cuda.set_per_process_memory_fraction.side_effect = AssertionError("CUDA is unavailable")
        modules = {
            "numpy": numpy,
            "torch": torch,
            "torch.nn": torch.nn,
            "torch.nn.functional": torch.nn.functional,
            "transformers": MagicMock(),
        }
        with patch.dict(sys.modules, modules):
            require_ml()
        torch.cuda.set_per_process_memory_fraction.assert_not_called()

    def test_cuda_torch_keeps_memory_limit(self):
        torch = MagicMock()
        torch.cuda.is_available.return_value = True
        with patch.dict(
            sys.modules,
            {
                "numpy": numpy,
                "torch": torch,
                "torch.nn": torch.nn,
                "torch.nn.functional": torch.nn.functional,
                "transformers": MagicMock(),
            },
        ):
            require_ml()
        torch.cuda.set_per_process_memory_fraction.assert_called_once_with(0.85)

    def test_onnx_banner_does_not_pollute_protocol_stdout(self):
        stdout, stderr = io.StringIO(), io.StringIO()
        hub = MagicMock()
        hub.hf_hub_download.side_effect = ValueError("stop before model download")
        with (
            patch("embedders.onnx_embed.require_ml_onnx", return_value=(None, None, None, None)),
            patch("embedders.onnx_embed._resolve_providers", return_value=(["CPUExecutionProvider"], "cpu")),
            patch.dict(sys.modules, {"huggingface_hub": hub}),
            contextlib.redirect_stdout(stdout),
            contextlib.redirect_stderr(stderr),
            self.assertRaises(SystemExit),
        ):
            EmbeddingGeneratorOnnx()
        self.assertEqual(stdout.getvalue(), "")
        self.assertIn("Using ONNX-RUNTIME", stderr.getvalue())
