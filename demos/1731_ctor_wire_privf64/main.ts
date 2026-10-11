class F { #r: f64 = 0; get(): f64 { return this.#r; } constructor() { this.#r = 2.5; } }
function main(): i32 {
  const f = new F();
  console.log(f.get());
  return 0;
}
