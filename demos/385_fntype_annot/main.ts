type F = (x: i32) => i32;
function main(): i32 {
  const f: (x: i32) => i32 = (x: i32): i32 => { return x + 1; };
  let h: (x: i32) => i32 = function (x: i32): i32 { return x + 2; };
  let g: F = (x: i32): i32 => { return x * 2; };
  console.log(f(41));
  console.log(h(41));
  console.log(g(21));
  return 0;
}
