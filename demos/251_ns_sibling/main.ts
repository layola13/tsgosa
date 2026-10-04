namespace N {
  export const K: number = 7;
  export function f(x: i32): i32 {
    return x + K;
  }
}
function main(): i32 {
  return N.f(40) + N.K;
}
console.log(main());
