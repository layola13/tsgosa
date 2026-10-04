class Pair {
  a: i32;
  b: i32;
  constructor(a: i32, b: i32) {
    this.a = a;
    this.b = b;
  }
  sum(): i32 {
    return this.a + this.b;
  }
}
function main(): i32 {
  const p = new Pair(11, 22);
  console.log(p.sum(), p.a, p.b);
  return 0;
}