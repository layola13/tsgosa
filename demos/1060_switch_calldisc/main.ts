function k(): i32 { return 2; }
function main(): i32 {
  switch (k()) {
    case 1: console.log(1); break;
    case 2: console.log(2); break;
    default: console.log(0);
  }
  return 0;
}
