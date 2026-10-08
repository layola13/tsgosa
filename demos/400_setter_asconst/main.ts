class C {
  x: i32 = 0;
  set v(n: i32) {
    this.x = n;
  }
  get v(): i32 {
    return this.x;
  }
}
function main(): i32 {
  const c = new C();
  c.v = 9;
  console.log(c.v);
  const a = [1, 2] as const;
  console.log(a[0] + a[1]);
  return 0;
}
