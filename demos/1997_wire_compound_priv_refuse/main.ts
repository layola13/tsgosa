class C {
  #p: number = 4;
  constructor(k: number) { this.#p += k; }
}
function main(): i32 {
  const c = new C(5);
  console.log(1);
  return 0;
}
