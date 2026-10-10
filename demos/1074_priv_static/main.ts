class C {
  static #K = 8;
  static getK(): i32 { return C.#K; }
}
function main(): i32 {
  console.log(C.getK());
  return 0;
}
