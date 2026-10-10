interface P { x: i32 }
const O: P = {x: 1};
function main(): i32 {
  console.log(typeof O);
  if (O) {
    console.log(!O ? 7 : 8);
  }
  return 0;
}
