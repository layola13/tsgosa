namespace N {
  export function f(): i32 { return 1; }
}
function main(): i32 {
  return N.f();
}
console.log(main());
