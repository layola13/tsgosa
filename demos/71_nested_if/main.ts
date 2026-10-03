function sign(n: i32): i32 {
  if (n > 0) {
    return 1;
  } else {
    if (n < 0) {
      return 0 - 1;
    } else {
      return 0;
    }
  }
}
function main(): i32 {
  console.log(sign(5));
  console.log(sign(0 - 3));
  console.log(sign(0));
  return 0;
}
