class F { #n: i32 = 0; get(): i32 { return this.#n; } constructor() { this.#n = 6 * 7; } }
function main(): i32 {
  const f = new F();
  console.log(f.get());
  return 0;
}
