class Vault {
  #x: i32;
  constructor(x: i32) {
    this.#x = x;
  }
  read(): i32 {
    return this.#x;
  }
}
function main(): i32 {
  const v = new Vault(42);
  console.log(v.read());
  return 0;
}
