class C {
  #p: number = 4;
  n: number = 0;
  constructor(k: number) { this.n = this.#p + k; }
}
function main(): i32 {
  const c = new C(5);
  console.log(c.n);
  return 0;
}
