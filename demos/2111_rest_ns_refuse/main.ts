namespace N {
  export function f(): i32 { return 1; }
  export function g(): i32 { return 2; }
}
function main(): i32 {
  const { f, ...rest } = N;
  return f();
}
