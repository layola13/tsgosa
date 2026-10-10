interface P { x: i32 }
const O: P = {x: 9} as const;
function main(): i32 {
  console.log(O.x);
  return 0;
}
