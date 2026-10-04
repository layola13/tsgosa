function grade(s: i32): i32 {
  switch (s) {
    case 90: {
      return 1;
    }
    case 80: {
      return 2;
    }
    default: {
      return 3;
    }
  }
}
function main(): i32 {
  console.log(grade(90), grade(80), grade(70));
  return 0;
}
