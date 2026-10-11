class F { a: boolean = false; b: i32 = 0; constructor() { this.a = !!true; this.b = ~5; } }
function main(): i32 {
  const f = new F();
  console.log(f.a ? 1 : 0);
  console.log(f.b);
  return 0;
}
