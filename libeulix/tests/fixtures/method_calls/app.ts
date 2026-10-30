import { Base } from './base';
import * as api from './helpers';
import * as ext from 'external_missing';
function send() { return 0; }
function inherited() { return 0; }
class Client extends Base {
  send() { return 1; }
  run() { this.send(); this.inherited(); }
  factory() { return this.send(); }
  bare() { return send(); }
  static static_call() { return send(); }
  missing() { return this.only_elsewhere(); }
  shadow(send: any) { return this.send(); }
}
class Other {
  send() { return 2; }
  unique() { return 3; }
  only_elsewhere() { return 4; }
}
function unknown(obj: any) { obj.send(); obj.unique(); }
function imported() { api.helper(); ext.unique(); }
function helper() { return 9; }
