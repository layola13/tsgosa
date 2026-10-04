namespace N {
  export let K: number = 7;
  export function bump(): i32 {
    K = K + 1;
    return K;
  }
}
function main(): i32 {
  return N.bump() + N.bump();
}
console.log(main());
