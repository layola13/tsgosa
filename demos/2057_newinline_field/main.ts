class C {
  n: number = 0;
  constructor(k: number) { this.n = k * 2; }
}
function main(): i32 {
  console.log(new C(21).n);
  return 0;
}
