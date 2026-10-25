class BucketBatchMixin:
    batch_size: int

    def batch_size_for(self, seq_len: int) -> int:
        """
        Bucket-aware batch size. self.batch_size is tuned for a reference
        sequence length of 512 tokens; scale down linearly for longer
        buckets (attention memory scales with sequence length), floor at 1.
        """
        ref_len = 512
        if seq_len <= ref_len:
            return self.batch_size
        return max(1, (self.batch_size * ref_len) // seq_len)
