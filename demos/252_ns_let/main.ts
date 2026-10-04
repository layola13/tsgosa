namespace N {
  export let K: number = 7;
  export function f(x: i32): i32 {
    return x + N.K;
  }
}
function main(): i32 {
  return N.f(40) + N.K;
}
console.log(main());
