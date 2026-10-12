function bump(): number { return 2; }
class C {
  n: number = 1;
  constructor(k: number) { this.n += bump(); }
}
function main(): i32 {
  const c = new C(5);
  console.log(c.n);
  return 0;
}
