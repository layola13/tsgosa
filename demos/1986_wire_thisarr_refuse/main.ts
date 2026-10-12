class C {
  arr: number[] = [1, 2];
  n: number = 0;
  constructor() { this.n = this.arr; }
}
function main(): i32 {
  const c = new C();
  console.log(c.n);
  return 0;
}
