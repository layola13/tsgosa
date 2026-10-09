class C { _v: i32 = 4; get v(): i32 { return this._v; } set v(x: i32) { this._v = x; } }
function main(): i32 {
  const c = new C();
  console.log(c.v);
  c.v = 9;
  console.log(c.v);
  return 0;
}
