import { z } from "zod";

// ==========================================
// 1. 基础标量、字面量与枚举 (Primitives & Enums)
// ==========================================

const RoleEnum = z.enum(["ADMIN", "USER", "MODERATOR", "GUEST"]);

// 支持原生 TypeScript enum
enum NativeStatus {
  DRAFT = "draft",
  PUBLISHED = "published",
  ARCHIVED = "archived",
}
const NativeStatusSchema = z.nativeEnum(NativeStatus);

// 字面量与特殊类型
const ProtocolSchema = z.literal("https");
const NullableOrUndefinedSchema = z.string().nullable().nullish(); // null | undefined | string
const UnknownDataSchema = z.unknown();
const AnyDataSchema = z.any();
const VoidReturnSchema = z.void();

// ==========================================
// 2. 字符串与数值的高级校验 (Validations)
// ==========================================

const UsernameSchema = z
  .string({
    required_error: "Username is required",
    invalid_type_error: "Username must be a string",
  })
  .trim()
  .min(3, "At least 3 characters")
  .max(20, "At most 20 characters")
  .regex(/^[a-zA-Z0-9_]+$/, "Alphanumeric and underscores only");

const EmailSchema = z.string().email("Invalid email format").toLowerCase();
const WebsiteSchema = z.string().url().optional();
const UuidSchema = z.string().uuid();
const DateTimeSchema = z.string().datetime(); // ISO 8601 校验

const AgeSchema = z
  .number()
  .int("Must be an integer")
  .positive("Must be > 0")
  .gte(18, "Must be at least 18")
  .lte(120, "Must be under 120")
  .finite();

const BigIntSchema = z.bigint().nonnegative();

// ==========================================
// 3. 数组、元组、Map 与 Set (Collections)
// ==========================================

// Array: 至少 1 个标签，最多 5 个标签
const TagsSchema = z.array(z.string().min(1)).nonempty("Must have at least one tag").max(5);

// Tuple: 固定长度与特定位置类型（带可选元素/rest 元素）
const CoordinateTupleSchema = z.tuple([
  z.number(), // 纬度
  z.number(), // 经度
  z.number().optional(), // 海拔（可选）
]);

// Set 与 Map
const UniqueTagsSchema = z.set(z.string()).min(1);
const MetadataMapSchema = z.map(z.string(), z.union([z.string(), z.number()]));

// Record: 任意键值映射 (k: string, v: unknown)
const ConfigRecordSchema = z.record(z.string(), z.boolean());

// ==========================================
// 4. 联合、交叉与可辨识联合 (Unions & Intersections)
// ==========================================

// 普通联合 (Union)
const StringOrNumber = z.union([z.string(), z.number()]);

// 可辨识联合 (Discriminated Union) - 性能更优，推导更精准
const PaymentMethodSchema = z.discriminatedUnion("type", [
  z.object({
    type: z.literal("CREDIT_CARD"),
    cardNumber: z.string().regex(/^\d{16}$/, "Must be 16 digits"),
    cvv: z.string().length(3),
  }),
  z.object({
    type: z.literal("PAYPAL"),
    paypalEmail: z.string().email(),
  }),
  z.object({
    type: z.literal("CRYPTO"),
    walletAddress: z.string().startsWith("0x"),
  }),
]);

// 交叉类型 (Intersection)
const TimestampBaseSchema = z.object({
  createdAt: z.date().default(() => new Date()),
  updatedAt: z.date().optional(),
});

// ==========================================
// 5. 递归与懒加载模式 (Recursive / Lazy)
// ==========================================

interface CategoryNode {
  id: string;
  name: string;
  subcategories: CategoryNode[];
}

const CategorySchema: z.ZodType<CategoryNode> = z.lazy(() =>
  z.object({
    id: z.string().uuid(),
    name: z.string().min(1),
    subcategories: z.array(CategorySchema),
  })
);

// ==========================================
// 6. 对象操作、变换 (Transform) 与 自定义校验 (Refine)
// ==========================================

const BaseUserSchema = z.object({
  id: UuidSchema,
  username: UsernameSchema,
  email: EmailSchema,
  role: RoleEnum.default("USER"),
  status: NativeStatusSchema.default(NativeStatus.DRAFT),
  age: AgeSchema,
  score: z.number().catch(0), // 校验失败时降级兜底为 0
  tags: TagsSchema,
  metadata: MetadataMapSchema.optional(),
  payment: PaymentMethodSchema,
  registeredAt: z.coerce.date(), // 自动强制类型转换 (String/Number -> Date)
  ipAddress: z.string().ip().optional(),
});

// 对象派生工具：.extend, .pick, .omit, .partial, .deepPartial, .passthrough, .strict
const ExtendedUserSchema = BaseUserSchema.extend({
  bio: z.string().max(200).default(""),
})
  .merge(TimestampBaseSchema)
  .passthrough(); // 允许额外未声明的 key 通过（默认是 strip 剔除；strict 则会报错）

// 密码确认联动校验 (superRefine) + 数据清洗转换 (transform)
const UserRegistrationSchema = ExtendedUserSchema.extend({
  password: z.string().min(8, "Password must be >= 8 chars"),
  confirmPassword: z.string(),
  // 字符串转数字再输出
  inviteCode: z
    .string()
    .transform((val) => val.toUpperCase().trim())
    .pipe(z.string().length(6, "Invite code must be exactly 6 characters")),
})
  // superRefine: 处理多字段联动校验、精细化路径报错
  .superRefine((data, ctx) => {
    if (data.password !== data.confirmPassword) {
      ctx.addIssue({
        code: z.ZodIssueCode.custom,
        message: "Passwords do not match",
        path: ["confirmPassword"], // 错误定位到具体字段
      });
    }

    if (data.role === "ADMIN" && data.age < 21) {
      ctx.addIssue({
        code: z.ZodIssueCode.custom,
        message: "Admins must be at least 21 years old",
        path: ["age"],
      });
    }
  })
  // transform: 校验成功后剥离冗余字段
  .transform((data) => {
    const { confirmPassword, ...sanitized } = data;
    return sanitized;
  });

// ==========================================
// 7. 异步校验 (Async Refine)
// ==========================================

const AsyncAccountSchema = z.object({
  username: z.string().refine(async (name) => {
    // 模拟数据库异步查重
    await new Promise((resolve) => setTimeout(resolve, 50));
    return name !== "forbidden_admin";
  }, "Username is already taken"),
});

// ==========================================
// 8. 类型推导 (Type Inference)
// ==========================================

// 输入类型（经过 transform / default 之前）
export type UserRegistrationInput = z.input<typeof UserRegistrationSchema>;

// 输出类型（经过 transform / default 之后）
export type UserRegistrationOutput = z.infer<typeof UserRegistrationSchema>;

// ==========================================
// 9. 运行期解析与错误处理 (Parse / SafeParse)
// ==========================================

const rawPayload = {
  id: "550e8400-e29b-41d4-a716-446655440000",
  username: "octocat",
  email: "HELLO@EXAMPLE.COM",
  age: 25,
  score: "invalid_number", // 会触发 .catch(0)
  tags: ["typescript", "zod"],
  payment: {
    type: "CREDIT_CARD",
    cardNumber: "1234567812345678",
    cvv: "999",
  },
  registeredAt: "2026-05-01T08:00:00.000Z", // z.coerce.date() 自动转换
  password: "super_secret_password_123",
  confirmPassword: "super_secret_password_123",
  inviteCode: "abc123 ", // transform 为 ABC123
  extraField: "retained because of .passthrough()",
};

// 同步安全解析 safeParse
const result = UserRegistrationSchema.safeParse(rawPayload);

if (!result.success) {
  // 扁平化提取错误信息
  const formattedErrors = result.error.flatten();
  console.error("Validation failed:", formattedErrors.fieldErrors);
} else {
  // 打印解析并清洗后的强类型数据
  console.log("Validated & Sanitized Data:", result.data);
}

// 异步安全解析 safeParseAsync
async function validateAsyncAccount(payload: unknown) {
  const asyncResult = await AsyncAccountSchema.safeParseAsync(payload);
  return asyncResult;
}
