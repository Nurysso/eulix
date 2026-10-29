from base import Base
import helpers as api
import external_missing as ext

def send(): return 0

def inherited(): return 0

class Client(Base):
    def send(self): return 1
    def run(self):
        self.send()
        self.inherited()
    @classmethod
    def factory(cls): return cls.send()
    def bare(self): return send()
    @staticmethod
    def static_call(): return send()
    def missing(self): return self.only_elsewhere()
    def shadow(self, send): return self.send()

class Other:
    def send(self): return 2
    def unique(self): return 3
    def only_elsewhere(self): return 4

def unknown(obj):
    obj.send()
    obj.unique()

def imported():
    api.helper()
    ext.unique()

def helper(): return 9
