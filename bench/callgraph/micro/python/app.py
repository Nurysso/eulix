class Form:
    def save(self):
        self.validate()
    def validate(self):
        pass

def helper():
    pass

def run():
    f = Form()
    f.save()
    helper()

import os
def other(cb):
    cb()
    print(os.path.join("a", "b"))
    return [1].append(2)
