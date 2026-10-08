namespace N {
  export const x: i32 = 1;
}
namespace N {
  export const y: i32 = 2;
}
function main(): i32 {
  console.log(N.x + N.y);
  return 0;
}
