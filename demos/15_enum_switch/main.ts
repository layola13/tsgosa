enum Color {
  Red,
  Green,
  Blue,
}
function classify(c: i32): i32 {
  let r: i32 = 0;
  switch (c) {
    case 0: {
      r = 10;
      break;
    }
    case 1: {
      r = 20;
      break;
    }
    default: {
      r = 30;
    }
  }
  return r;
}
function main(): i32 {
  console.log(classify(1));
  console.log(classify(0) + classify(2));
  return 0;
}
