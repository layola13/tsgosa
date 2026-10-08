class C {
  v: i32 = 0;
  get d(): i32 {
    return this.v * 2;
  }
  set d(n: i32) {
    this.v = n;
  }
}
function main(): i32 {
  const c = new C();
  c.d = 21;
  console.log(c.d);
  return 0;
}
