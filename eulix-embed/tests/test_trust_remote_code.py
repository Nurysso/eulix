# Copyright (C) 2026 Dawood K.
# SPDX-License-Identifier: Apache-2.0

import os
import tempfile
import unittest

from embedders.torch_embed import trusts_remote_code


class TrustRemoteCodeTests(unittest.TestCase):
    # A repository's eulix.toml picks the model, and trust_remote_code runs
    # Python from the model's repository: a hostile name must never get it.
    def test_only_jinas_own_hub_models_run_repository_code(self):
        for name in ("jinaai/jina-embeddings-v2-base-code", "JinaAI/jina-embeddings-v3"):
            self.assertTrue(trusts_remote_code(name), name)
        for name in ("attacker/jina-embeddings-v2", "jina-embeddings-v2", "BAAI/bge-base-en-v1.5", "jinaai", "./jinaai/x"):
            self.assertFalse(trusts_remote_code(name), name)

    def test_a_local_directory_named_like_a_jina_model_is_not_trusted(self):
        with tempfile.TemporaryDirectory() as tmp:
            cwd = os.getcwd()
            os.chdir(tmp)
            try:
                os.makedirs("jinaai/evil")  # shipped inside a cloned repository
                self.assertFalse(trusts_remote_code("jinaai/evil"))
            finally:
                os.chdir(cwd)


if __name__ == "__main__":
    unittest.main()
