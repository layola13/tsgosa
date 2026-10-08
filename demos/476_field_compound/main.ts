class C {
  n: i32 = 10;
  bump(): void {
    this.n += 1;
  }
}
interface Ctx {
  n: i32;
}
function bumped(ctx: Ctx): void {
  ctx.n += 1;
}
function main(): i32 {
  const c = new C();
  c.bump();
  c.n += 5;
  console.log(c.n);
  const o: Ctx = { n: 10 };
  bumped(o);
  console.log(o.n);
  return 0;
}
