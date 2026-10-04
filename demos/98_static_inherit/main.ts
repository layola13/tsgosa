class Base {
  static tag: i32 = 7;
  static who(): i32 {
    return 11;
  }
}
class Sub extends Base {
}
function main(): i32 {
  console.log(Sub.tag, Sub.who());
  return 0;
}
