class C {
  _v: i32;
  constructor(v: i32) { this._v = v; }
  set v(x: i32) { this._v = x + 1; }
  get v(): i32 { return this._v; }
}
function main(): i32 {
  const c = new C(0);
  c.v = 10;
  c.v = 20;
  console.log(c.v);
  return 0;
}
